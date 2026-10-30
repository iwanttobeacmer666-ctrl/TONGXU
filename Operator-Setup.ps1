# Dot-source from Start-GroupAssistant.ps1. No connection or credential read on load.
Set-StrictMode -Version Latest
. (Join-Path $PSScriptRoot 'Operator-Paths.ps1')

function Read-SetupText([string]$Prompt, [string]$Default = '') {
    $answer = Read-Host "$Prompt $(if ($Default) { "[$Default]" }) (/cancel 取消)"
    if ($answer -eq '/cancel') { throw [OperationCanceledException]::new('已取消，原设置未改变。') }
    if ($answer -eq '' -and $Default -ne '') { return $Default }
    return $answer.Trim()
}

function Assert-SetupID([string]$Value) {
    if (-not $Value -or $Value.Length -gt 250 -or $Value -ne $Value.Trim() -or
        $Value -match '[\x00-\x20\x7f]' -or $Value.StartsWith('YOUR_') -or $Value.StartsWith('wecom:')) {
        throw 'ID 无效：请原样输入平台稳定 ID，不要使用昵称、空格或 wecom: 前缀。'
    }
}

function Read-SetupCount([string]$Prompt, [int]$Minimum, [int]$Maximum) {
    $value = 0
    $text = Read-SetupText $Prompt
    if (-not [int]::TryParse($text, [ref]$value) -or $value -lt $Minimum -or $value -gt $Maximum) {
        throw "请输入 $Minimum 至 $Maximum 之间的整数。"
    }
    return $value
}

function Read-SetupSecret {
    $secret = Read-Host '机器人 Secret（隐藏输入；取消请按 Ctrl+C）' -AsSecureString
    try {
        if ($secret.Length -eq 0) { throw 'Secret 不能为空。' }
        return ConvertFrom-SecureString -SecureString $secret
    } finally { $secret.Dispose() }
}

function Assert-OperatorSetup($Setup, $Base) {
    $initialOwner = $null -ne $Setup.PSObject.Properties['OwnerInitialized'] -and $Setup.OwnerInitialized -eq $true
    if ($Setup.Version -ne 1 -or (-not $initialOwner -and $Setup.Owner -cne $Base.Owner)) { throw '设置版本或管理员不匹配；不自动转交管理员。' }
    Assert-SetupID $Setup.Owner
    Assert-SetupID $Setup.BotID
    if (-not $Setup.ProtectedSecret -or $Setup.ProtectedSecret.Length -gt 32768) { throw '加密凭据缺失或过长。' }
    if (@($Setup.Groups).Count -lt 1 -or @($Setup.Groups).Count -gt 16) { throw '请配置 1 至 16 个群（包括停用群）。' }
    if ($null -ne $Setup.PSObject.Properties['Paths']) { $null = Merge-OperatorPaths $Base $Setup }
    $groups = [Collections.Generic.HashSet[string]]::new([StringComparer]::Ordinal)
    foreach ($group in $Setup.Groups) {
        Assert-SetupLifecycle $group
        Assert-SetupID $group.ID
        if ($group.Enabled -isnot [bool] -or -not $groups.Add($group.ID)) { throw '群 ID 重复或启用状态无效。' }
        $users = [Collections.Generic.HashSet[string]]::new([StringComparer]::Ordinal)
        foreach ($user in @($group.Members)) {
            Assert-SetupLifecycle $user
            Assert-SetupID $user.ID
            if ($user.ID -ceq $Setup.Owner -or $user.Enabled -isnot [bool] -or -not $users.Add($user.ID)) {
                throw '普通成员 ID 重复、包含管理员或启用状态无效。'
            }
        }
    }
}

function Assert-SetupLifecycle($Entry) {
    if ($null -eq $Entry.PSObject.Properties['State'] -or -not $Entry.State) { return }
    if (($Entry.Enabled -and $Entry.State -cne 'enabled') -or
        (-not $Entry.Enabled -and $Entry.State -cnotin @('disabled','disabling','cleanup_failed','deleting','delete_failed'))) {
        throw '启用状态与任务清理状态不一致。'
    }
}

function Switch-SetupEnabled($Entry) {
    if ($null -ne $Entry.PSObject.Properties['State'] -and $Entry.State -cnotin @('','enabled','disabled')) {
        throw '任务清理或删除尚未完成，请通过本地管理接口重新核验后再启用。'
    }
    $Entry.Enabled = -not $Entry.Enabled
    if ($null -ne $Entry.PSObject.Properties['State']) { $Entry.State = if ($Entry.Enabled) { 'enabled' } else { 'disabled' } }
}

function Read-OperatorSetup([string]$Path, $Base) {
    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) { return $null }
    if ((Get-Item -LiteralPath $Path).Length -gt 262144) { throw '设置文件过大；拒绝加载，不自动重置。' }
    try { $setup = Get-Content -LiteralPath $Path -Raw -Encoding UTF8 | ConvertFrom-Json }
    catch { throw '设置文件不能解析；原文件保留，不自动重置。' }
    Assert-OperatorSetup $setup $Base
    return $setup
}

function Open-OperatorLock([string]$Directory,[string]$Name='operator.lock') {
    try { return [IO.File]::Open((Join-Path $Directory $Name),'OpenOrCreate','ReadWrite','None') }
    catch { throw '配置或服务正在切换，请稍后刷新；未修改设置。' }
}

function Read-OperatorSnapshot([string]$Path,$Base) {
    $lock=Open-OperatorLock ([IO.Path]::GetDirectoryName($Path))
    try {
        $setup=Read-OperatorSetup $Path $Base
        $version=if(Test-Path -LiteralPath $Path -PathType Leaf){(Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash.ToLowerInvariant()}else{''}
        return [pscustomobject]@{Setup=$setup;Version=$version}
    } finally { $lock.Dispose() }
}

function New-EffectiveConfig($Base, $Setup, [switch]$Formal) {
    Assert-OperatorSetup $Setup $Base
    Assert-OperatorPaths $Base $Setup
    $config = Merge-OperatorPaths $Base $Setup
    $active = @($Setup.Groups | Where-Object { $_.Enabled })
    # An empty active roster keeps local management available while denying
    # every incoming group request; groups can be enabled live afterwards.
    $members = [ordered]@{}
    foreach ($group in $active) {
        $members.Add($group.ID, @($group.Members | Where-Object { $_.Enabled } | ForEach-Object { $_.ID }))
    }
    $config.Groups = @($active | ForEach-Object { $_.ID })
    $config | Add-Member -NotePropertyName Members -NotePropertyValue $members -Force
    # Only an explicit formal launch opts in, for this process. Never persist
    # an acceptance claim in the base or saved operator settings.
    $config.BoundaryVerified = [bool]$Formal
    $config | Add-Member -NotePropertyName OwnerOnly -NotePropertyValue (-not [bool]$Formal) -Force
    return $config
}

function Write-PrivateJson([string]$Path, $Value) {
    $json = $Value | ConvertTo-Json -Depth 20
    [IO.File]::WriteAllText($Path, $json, [Text.UTF8Encoding]::new($false))
}

function Test-EffectiveConfig([string]$Binary, $Base, $Setup, [string]$Directory) {
    # Allow storing an entirely disabled roster; validate its paths and members
    # with a temporary all-enabled copy. Startup still rejects zero active groups.
    $validation = $Setup | ConvertTo-Json -Depth 20 | ConvertFrom-Json
    foreach ($group in $validation.Groups) { $group.Enabled = $true }
    $path = Join-Path $Directory ('.operator-check-' + [guid]::NewGuid().ToString('N') + '.json')
    try {
        Write-PrivateJson $path (New-EffectiveConfig $Base $validation)
        if ((Get-Item -LiteralPath $path).Length -gt 65536) { throw '有效配置超过程序支持的 64 KiB；请缩减名单。' }
        $checkOutput = & $Binary -config $path -check
        if ($LASTEXITCODE -ne 0) { throw '配置检查失败，原设置未改变。' }
    } finally { if (Test-Path -LiteralPath $path) { Remove-Item -LiteralPath $path } }
}

function Show-OperatorSetup($Setup, $Base = $null) {
    Write-Host "管理员：$($Setup.Owner)；Bot ID：$($Setup.BotID)；Secret：已加密保存（不显示）"
    $i = 0
    foreach ($group in $Setup.Groups) {
        $i++
        $enabled = @($group.Members | Where-Object { $_.Enabled }).Count
        Write-Host "$i. 群 $($group.ID)；启用=$($group.Enabled)；启用普通成员=$enabled；连同管理员=$($enabled + 1)"
        $j = 0
        foreach ($user in @($group.Members)) { $j++; Write-Host "   $j. $($user.ID)；普通成员；启用=$($user.Enabled)" }
    }
    if ($null -ne $Base) {
        $config = Merge-OperatorPaths $Base $Setup
        foreach ($key in @('CodexCommand','DataDir','WorkDir','SoftwareDir','StateDir','CodexHome')) { Write-Host "$key : $($config.$key)" }
    }
}

function Save-OperatorSetup([string]$Path, $Setup, $Base, [string]$Binary, [string]$ExpectedVersion) {
    $saveLock=Open-OperatorLock ([IO.Path]::GetDirectoryName($Path))
    $serviceLock=$null
    try {
    $serviceLock=Open-OperatorLock ([IO.Path]::GetDirectoryName($Path)) 'service.lock'
    $currentVersion=if(Test-Path -LiteralPath $Path -PathType Leaf){(Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash.ToLowerInvariant()}else{''}
    if($PSBoundParameters.ContainsKey('ExpectedVersion') -and $ExpectedVersion -cne $currentVersion){throw '配置已由其他窗口更新，请刷新后重新修改，原设置未覆盖。'}
    if (Test-Path -LiteralPath $Path -PathType Leaf) {
        $existing = Read-OperatorSetup $Path $Base
        if ($existing.Owner -cne $Setup.Owner) { throw '已有部署不支持管理员转交，原设置保留。' }
    }
    Assert-OperatorSetup $Setup $Base
    Test-EffectiveConfig $Binary $Base $Setup ([IO.Path]::GetDirectoryName($Path))
    Show-OperatorSetup $Setup $Base
    if ((Read-SetupText '确认保存以上设置？输入 SAVE') -cne 'SAVE') { return $false }
    $temp = $Path + '.' + [guid]::NewGuid().ToString('N') + '.tmp'
    try {
        Write-PrivateJson $temp $Setup
        if ((Get-Item -LiteralPath $temp).Length -gt 262144) { throw '设置文件过大，未保存。' }
        if (Test-Path -LiteralPath $Path) { [IO.File]::Replace($temp, $Path, [NullString]::Value) }
        else { [IO.File]::Move($temp, $Path) }
    } finally { if (Test-Path -LiteralPath $temp) { Remove-Item -LiteralPath $temp } }
    Write-Host '已保存；独立配置入口不会连接群，正常启动入口将继续启动。'
    return $true
    } finally { if($null -ne $serviceLock){$serviceLock.Dispose()};$saveLock.Dispose() }
}

function Read-SetupMembers([string]$Owner) {
    $count = Read-SetupCount '普通成员有几位？（不含管理员，允许 0）' 0 ([int]::MaxValue)
    $members = @()
    $seen = [Collections.Generic.HashSet[string]]::new([StringComparer]::Ordinal)
    for ($i = 1; $i -le $count; $i++) {
        $id = Read-SetupText "第 $i 位普通成员的稳定用户 ID"
        Assert-SetupID $id
        if ($id -ceq $Owner -or -not $seen.Add($id)) { throw '成员重复或与管理员相同，未保存。' }
        $members += [pscustomobject]@{ ID = $id; Enabled = $true }
    }
    return $members
}

function New-OperatorSetup($Base) {
    Write-Host "现有管理员：$($Base.Owner)；现有群：$($Base.Groups -join ', ')。文件和会话将保留。"
    $bot = Read-SetupText '请输入机器人 Bot ID'
    Assert-SetupID $bot
    $protected = Read-SetupSecret
    $owner = Read-SetupText '首次部署管理员稳定用户 ID（回车沿用）' $Base.Owner
    Assert-SetupID $owner
    $count = Read-SetupCount '请输入群数量' 1 16
    $groups = @()
    for ($i = 0; $i -lt $count; $i++) {
        $default = if ($i -lt @($Base.Groups).Count) { $Base.Groups[$i] } else { '' }
        $id = Read-SetupText "第 $($i + 1) 个群 ID" $default
        Assert-SetupID $id
        $groups += [pscustomobject]@{ ID = $id; Enabled = $true; Members = @(Read-SetupMembers $owner) }
    }
    $paths = Read-OperatorPaths $Base -ChooseCodex
    return [pscustomobject]@{ Version = 1; Owner = $owner; OwnerInitialized = $true; BotID = $bot; ProtectedSecret = $protected; Groups = $groups; Paths = $paths }
}

function Edit-OperatorSetup($Current, $Base = $null) {
    $draft = $Current | ConvertTo-Json -Depth 20 | ConvertFrom-Json
    Show-OperatorSetup $draft $Base
    Write-Host '1 更换机器人凭据；2 添加群；3 修改群 ID；4 启用/停用群；5 添加成员；6 修改成员 ID；7 启用/停用成员；8 Codex 与工作目录；0 返回'
    $action = Read-SetupText '请选择'
    if ($action -eq '0') { return $null }
    if ($action -eq '8') {
        if ($null -eq $Base) { throw '路径管理需要基础配置。' }
        $paths = Read-OperatorPaths $Base $draft -ChooseCodex
        $draft | Add-Member -NotePropertyName Paths -NotePropertyValue $paths -Force
        return $draft
    }
    if ($action -eq '1') {
        $draft.BotID = Read-SetupText 'Bot ID' $draft.BotID
        $draft.ProtectedSecret = Read-SetupSecret
        return $draft
    }
    if ($action -eq '2') {
        $id = Read-SetupText '新增群 ID'
        Assert-SetupID $id
        $draft.Groups = @($draft.Groups) + [pscustomobject]@{ ID = $id; Enabled = $true; Members = @(Read-SetupMembers $draft.Owner) }
        return $draft
    }
    if ($action -notin @('3','4','5','6','7')) { throw '无效选项。' }
    $index = (Read-SetupCount '群编号' 1 @($draft.Groups).Count) - 1
    $group = $draft.Groups[$index]
    switch ($action) {
        '3' {
            Write-Host '群 ID 改变后视为另一个群，原群资料保留且不会迁移。请核对 ID。'
            $group.ID = Read-SetupText '新的群 ID' $group.ID
        }
        '4' { Switch-SetupEnabled $group }
        '5' { $group.Members = @($group.Members) + @(Read-SetupMembers $draft.Owner) }
        { $_ -in @('6','7') } {
            if (@($group.Members).Count -eq 0) { throw '这个群还没有普通成员。' }
            $user = $group.Members[(Read-SetupCount '成员编号（按以上显示顺序）' 1 @($group.Members).Count) - 1]
            if ($action -eq '7') { Switch-SetupEnabled $user }
            else {
                Write-Host '用户 ID 改变后视为另一位用户，原资料保留且不会迁移。'
                $user.ID = Read-SetupText '新的稳定用户 ID' $user.ID
            }
        }
    }
    return $draft
}

function Save-SelectedCodexUnderLock([string]$Path,$Base,[string]$Selected,[string]$ExpectedVersion) {
    # Caller owns operator.lock; also refuse writing while any service is live.
    $serviceLock=Open-OperatorLock ([IO.Path]::GetDirectoryName($Path)) 'service.lock'
    $temp=$Path+'.'+[guid]::NewGuid().ToString('N')+'.tmp'
    try {
        if (-not (Test-Path -LiteralPath $Path -PathType Leaf) -or (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash.ToLowerInvariant() -cne $ExpectedVersion) {
            throw '设置已变更，未覆盖路径。请重新启动后选择。'
        }
        $current=Read-OperatorSetup $Path $Base
        if ($null -eq $current.PSObject.Properties['Paths']) { $current | Add-Member -NotePropertyName Paths -NotePropertyValue ([pscustomobject]@{}) }
        if ($null -ne $current.Paths.PSObject.Properties['CodexCommand']) { $current.Paths.CodexCommand=$Selected }
        else { $current.Paths | Add-Member -NotePropertyName CodexCommand -NotePropertyValue $Selected }
        Assert-OperatorSetup $current $Base
        Write-PrivateJson $temp $current
        if ((Get-Item -LiteralPath $temp).Length -gt 262144) { throw '设置文件过大，未保存路径。' }
        [IO.File]::Replace($temp,$Path,[NullString]::Value)
        Write-Host 'Codex 路径已保存；下次有效时直接启动，不再询问。'
    } finally {
        if(Test-Path -LiteralPath $temp){Remove-Item -LiteralPath $temp}
        $serviceLock.Dispose()
    }
}

function Start-OperatorService([string]$Binary, $Base, $Setup, [string]$Directory, [ValidateSet('Trial','Formal')][string]$Mode = 'Trial', [switch]$ChooseCodex) {
    $launchLock=Open-OperatorLock $Directory
    try {
    $latestPath=Join-Path $Directory 'operator-setup.json'
    if(Test-Path -LiteralPath $latestPath -PathType Leaf){$Setup=Read-OperatorSetup $latestPath $Base}
    $settingsVersion=if(Test-Path -LiteralPath $latestPath -PathType Leaf){(Get-FileHash -LiteralPath $latestPath -Algorithm SHA256).Hash.ToLowerInvariant()}else{''}
    $savedCodex=(Merge-OperatorPaths $Base $Setup).CodexCommand
    # Probe a deep copy; only a successful selection may update the saved path.
    $Setup = $Setup | ConvertTo-Json -Depth 20 | ConvertFrom-Json
    $selected = Select-NativeCodex (Merge-OperatorPaths $Base $Setup).CodexCommand -Choose:$ChooseCodex
    if ($null -eq $Setup.PSObject.Properties['Paths']) { $Setup | Add-Member -NotePropertyName Paths -NotePropertyValue ([pscustomobject]@{}) }
    $Setup.Paths | Add-Member -NotePropertyName CodexCommand -NotePropertyValue $selected -Force
    $formal = $Mode -ceq 'Formal'
    $effective = New-EffectiveConfig $Base $Setup -Formal:$formal
    $secure = $null
    $startInfo = [Diagnostics.ProcessStartInfo]::new()
    $startInfo.FileName = $Binary
    $startInfo.UseShellExecute = $false
    $path = Join-Path $Directory ('.operator-run-' + [guid]::NewGuid().ToString('N') + '.json')
    try {
        Write-PrivateJson $path $effective
        $checkOutput = & $Binary -config $path -check
        if ($LASTEXITCODE -ne 0) { throw '启动检查失败；未连接。' }
        $nativeCheckOutput = & $Binary -config $path -check-native
        if ($LASTEXITCODE -ne 0) { throw 'Codex 原生兼容/权限握手检查未通过；未连接群。请用 -ChooseCodex 或配置入口选择其他程序。' }
        try { $secure = ConvertTo-SecureString -String $Setup.ProtectedSecret -ErrorAction Stop }
        catch { throw 'Secret 无法由当前 Windows 用户解密。请在配置管理中重新输入；未连接。' }
        if ($settingsVersion -ne '' -and -not [string]::Equals([IO.Path]::GetFullPath($savedCodex),$selected,[StringComparison]::OrdinalIgnoreCase)) {
            Save-SelectedCodexUnderLock $latestPath $Base $selected $settingsVersion
        }
        # Credentials exist only in the new control process environment, never
        # parent/shell persistent variables. Native workers already filter them.
        $modeArgument = if ($formal) { '' } else { ' -acceptance-owner-only' }
        $startInfo.Arguments = '-config "' + $path + '"' + $modeArgument + ' -operator-settings "' + (Join-Path $Directory 'operator-setup.json') + '"'
        $startInfo.EnvironmentVariables['CC_GROUP_BOT_ID'] = $Setup.BotID
        $startInfo.EnvironmentVariables['CC_GROUP_BOT_SECRET'] = [Net.NetworkCredential]::new('', $secure).Password
        if ($formal) {
            Write-Host '即将启动正式多人服务：管理员及各群已登记、已启用成员可办公。Ctrl+C 停止。'
            Write-Host '本次选择启用多人运行，不代表真实群、附件及全部隔离项目已验收；请先用测试资料核验。'
        } else {
            Write-Host '即将连接管理员试用。请先完成专用 Codex 登录；Ctrl+C 停止。正式多人运行仍关闭。'
        }
        $process = [Diagnostics.Process]::Start($startInfo)
        try {
            $process.WaitForExit()
            if ($process.ExitCode -ne 0) { throw '服务进程异常退出，请检查本机错误提示。' }
        } finally {
            if (-not $process.HasExited) { $process.WaitForExit() }
            $process.Dispose()
        }
    } finally {
        $startInfo.EnvironmentVariables.Remove('CC_GROUP_BOT_SECRET')
        if ($null -ne $secure) { $secure.Dispose() }
        if (Test-Path -LiteralPath $path) { Remove-Item -LiteralPath $path }
    }
    } finally { $launchLock.Dispose() }
}

function Start-OperatorTrial([string]$Binary, $Base, $Setup, [string]$Directory) {
    Start-OperatorService $Binary $Base $Setup $Directory -Mode Trial
}
