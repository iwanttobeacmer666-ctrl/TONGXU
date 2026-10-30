param([ValidateSet('read','save','launch','login')][string]$Action='read',[string]$ProjectRoot=$PSScriptRoot)
$ErrorActionPreference='Stop'
$ProgressPreference='SilentlyContinue'
[Console]::InputEncoding=[Text.UTF8Encoding]::new($false)
[Console]::OutputEncoding=[Text.UTF8Encoding]::new($false)
$OutputEncoding=[Text.UTF8Encoding]::new($false)
. (Join-Path $PSScriptRoot 'Operator-Setup.ps1')
$taskRuntime=Join-Path $ProjectRoot 'runtime'
$taskSettings=Join-Path $taskRuntime 'operator-setup.json'
$taskBinary=Join-Path $ProjectRoot 'bin\group-assistant.exe'
function Get-DesktopSnapshot {
    if(-not(Test-Path -LiteralPath $taskSettings -PathType Leaf)){return [pscustomobject]@{Setup=$null;Version=''}}
    $taskBytes=[IO.File]::ReadAllBytes($taskSettings)
    if($taskBytes.Length -gt 262144){throw '设置文件过大。'}
    $taskValue=[Text.Encoding]::UTF8.GetString($taskBytes)|ConvertFrom-Json
    Assert-OperatorSetup $taskValue $taskBase
    $taskHasher=[Security.Cryptography.SHA256]::Create()
    try{$taskVersion=([BitConverter]::ToString($taskHasher.ComputeHash($taskBytes))).Replace('-','').ToLowerInvariant()}finally{$taskHasher.Dispose()}
    return [pscustomobject]@{Setup=$taskValue;Version=$taskVersion}
}
try {
    & $taskBinary -check-local-client 1>$null 2>$null
    if($LASTEXITCODE -ne 0){throw '当前进程不满足本机管理员控制边界，拒绝读取或修改设置。'}
    # Hosted Windows PowerShell can load the utility cmdlets without its
    # script functions. Load the already-installed manifest explicitly so the
    # original save path has Get-FileHash; no module installation/config change.
    Import-Module (Join-Path $PSHOME 'Modules\Microsoft.PowerShell.Utility\Microsoft.PowerShell.Utility.psd1') -ErrorAction Stop
    $taskBase=Get-OperatorBase $ProjectRoot
    $taskSnapshot=Get-DesktopSnapshot
    if($Action -eq 'read') {
        $taskEffective=Merge-OperatorPaths $taskBase $taskSnapshot.Setup
        $taskPaths=[ordered]@{};foreach($taskKey in $script:OperatorPathKeys){$taskPaths[$taskKey]=$taskEffective.$taskKey}
        $taskOwner=$taskEffective.Owner
        $taskHasSecret=$false;$taskBot='未配置'
        if($null -ne $taskSnapshot.Setup){$taskHasSecret=[bool]$taskSnapshot.Setup.ProtectedSecret;$taskID=[string]$taskSnapshot.Setup.BotID;$taskBot=if($taskID.Length -gt 4){'••••'+$taskID.Substring($taskID.Length-4)}else{'••••'}}
        [pscustomobject]@{Success=$true;Version=$taskSnapshot.Version;Owner=$taskOwner;BotIDMasked=$taskBot;HasCredentials=$taskHasSecret;Paths=$taskPaths;AgentFileExists=(Test-Path -LiteralPath $taskEffective.CodexCommand -PathType Leaf)}|ConvertTo-Json -Depth 8 -Compress
        return
    }
    if($Action -eq 'save') {
        $taskInput=[Console]::In.ReadToEnd()
        if([Text.Encoding]::UTF8.GetByteCount($taskInput) -gt 65536){throw '请求过大。'}
        $taskRequest=$taskInput|ConvertFrom-Json
        foreach($taskProperty in $taskRequest.PSObject.Properties){if($taskProperty.Name -notin @('Version','BotID','Secret','Paths','Owner','FirstGroup')){throw '不支持的设置字段。'}}
        if($null -eq $taskRequest.PSObject.Properties['Version'] -or $taskRequest.Version -cne $taskSnapshot.Version){throw '配置版本冲突，请刷新后重新核对。'}
        $taskDraft=$taskSnapshot.Setup
        if($null -eq $taskDraft){
            $taskInitialOwner=if($taskRequest.Owner){[string]$taskRequest.Owner}else{[string]$taskBase.Owner}
            Assert-SetupID $taskInitialOwner;Assert-SetupID $taskRequest.FirstGroup
            $taskDraft=[pscustomobject]@{Version=1;Owner=$taskInitialOwner;OwnerInitialized=$true;BotID='';ProtectedSecret='';Groups=@([pscustomobject]@{ID=$taskRequest.FirstGroup;Enabled=$true;Members=@()})}
        }
        if($null -ne $taskRequest.PSObject.Properties['BotID'] -and $taskRequest.BotID){$taskDraft.BotID=[string]$taskRequest.BotID}
        if($null -ne $taskRequest.PSObject.Properties['Secret'] -and $taskRequest.Secret){
            $taskSecret=ConvertTo-SecureString -String ([string]$taskRequest.Secret) -AsPlainText -Force
            try{$taskDraft.ProtectedSecret=ConvertFrom-SecureString -SecureString $taskSecret}finally{$taskSecret.Dispose()}
        }
        if($null -ne $taskRequest.PSObject.Properties['Paths'] -and $null -ne $taskRequest.Paths){
            if($null -eq $taskDraft.PSObject.Properties['Paths']){$taskDraft|Add-Member -NotePropertyName Paths -NotePropertyValue ([pscustomobject]@{})}
            foreach($taskPath in $taskRequest.Paths.PSObject.Properties){
                if($taskPath.Name -notin $script:OperatorPathKeys -or $taskPath.Value -isnot [string] -or -not $taskPath.Value){throw '不支持的路径字段。'}
                $taskDraft.Paths|Add-Member -NotePropertyName $taskPath.Name -NotePropertyValue $taskPath.Value -Force
            }
        }
        # GUI already presents an explicit save confirmation. Reuse all existing
        # version locks, DPAPI, validation and atomic-write rules without a console.
        function Show-OperatorSetup { }
        function Read-SetupText([string]$Prompt,[string]$Default=''){if($Prompt.StartsWith('确认保存')){return 'SAVE'};throw '需要交互配置，请返回桌面设置。'}
        $taskSaved=Save-OperatorSetup $taskSettings $taskDraft $taskBase $taskBinary $taskRequest.Version 6>$null
        if(-not $taskSaved){throw '设置未保存。'}
        [pscustomobject]@{Success=$true;Message='已保存，下次启动服务生效；未搬迁或删除旧资料。'}|ConvertTo-Json -Compress
        return
    }
    if($null -eq $taskSnapshot.Setup){throw '请先在桌面连接配置中保存设置。'}
    if($Action -eq 'launch'){
        function Read-SetupText([string]$Prompt,[string]$Default=''){throw '需要选择有效的 Codex 程序，请先在设置中完成配置。'}
        $taskLauncher=Open-OperatorLock $taskRuntime 'launcher.lock'
        try{Start-OperatorService $taskBinary $taskBase $taskSnapshot.Setup $taskRuntime -Mode Formal 1>$null 6>$null}finally{$taskLauncher.Dispose()}
        [pscustomobject]@{Success=$true;Message='服务进程已退出；如有清理失败，请重新核验临时区。'}|ConvertTo-Json -Compress
        return
    }
    if($Action -eq 'login'){& (Join-Path $ProjectRoot 'Login-Codex.ps1');return}
} catch {
    # Never return raw OS/native stderr or a credential-bearing request.
    $taskMessage=switch -Regex ($_.Exception.Message){'版本|冲突|更新' {'配置版本已变化，请刷新后重新操作。';break};'锁|正在|busy' {'服务或配置正在使用中，请先停止服务后重试。';break};'Secret|凭据|解密' {'凭据校验或解密失败，请重新输入机器人凭据；未连接。';break};'Codex|原生|握手' {'Codex 程序或启动检查未通过，请核对程序路径及专用登录；未自动修复。';break};'路径|目录|磁盘|重叠|工作|认证' {'目录校验未通过。请使用有效的本地专用目录，避免相互重叠或指向源码、工具及个人认证目录。';break};'管理员|ID|群|成员|设置|配置' {'设置校验未通过，请核对稳定 ID、群和成员。已有管理员不能转交。';break};default {'操作失败，请检查配置、服务状态及目录访问权限。'}}
    $taskTarget=if($_.FullyQualifiedErrorId -eq 'CommandNotFoundException'){$_.CategoryInfo.TargetName}else{''}
    [pscustomobject]@{Success=$false;Message=$taskMessage;Code=$_.FullyQualifiedErrorId;Line=$_.InvocationInfo.ScriptLineNumber;Target=$taskTarget}|ConvertTo-Json -Compress
    exit 1
}
