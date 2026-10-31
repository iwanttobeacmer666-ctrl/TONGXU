$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'Operator-Setup.ps1')
$taskManageBinary = Join-Path $PSScriptRoot 'bin\group-assistant.exe'
$taskManageSettings = Join-Path (Get-OperatorRuntime $PSScriptRoot) 'operator-setup.json'
if (-not (Test-Path -LiteralPath $taskManageBinary -PathType Leaf)) { throw '请先构建同序。' }
if (-not (Test-Path -LiteralPath $taskManageSettings -PathType Leaf)) { throw '请先用 Start-GroupAssistant.ps1 完成首次配置。' }

function Invoke-MemberManagement($Request) {
    # Only explicit management fields enter stdin. No shell expression, bot
    # secret, authentication data or arbitrary config is sent by this window.
    $json = $Request | ConvertTo-Json -Compress
    $start = [Diagnostics.ProcessStartInfo]::new()
    $start.FileName = $taskManageBinary
    $start.Arguments = '-manage-local -operator-settings "' + $taskManageSettings + '"'
    $start.UseShellExecute = $false
    $start.CreateNoWindow = $true
    $start.RedirectStandardInput = $true
    $start.RedirectStandardOutput = $true
    $start.RedirectStandardError = $true
    $start.StandardOutputEncoding = [Text.UTF8Encoding]::new($false)
    $start.StandardErrorEncoding = [Text.UTF8Encoding]::new($false)
    $process = [Diagnostics.Process]::Start($start)
    try {
        $bytes = [Text.Encoding]::UTF8.GetBytes($json)
        $process.StandardInput.BaseStream.Write($bytes,0,$bytes.Length)
        $process.StandardInput.Close()
        $stdout = $process.StandardOutput.ReadToEndAsync()
        $stderr = $process.StandardError.ReadToEndAsync()
        $process.WaitForExit()
        if ($process.ExitCode -ne 0) { throw ('本地管理未确认，请刷新核对，不要重复提交。客户端信息：' + $stderr.GetAwaiter().GetResult()) }
        return ($stdout.GetAwaiter().GetResult() | ConvertFrom-Json)
    } finally {
        if (-not $process.HasExited) { $process.Kill() }
        $process.Dispose()
    }
}

while ($true) {
    try {
        $taskView = Invoke-MemberManagement @{ Action='list' }
        if (-not $taskView.Success) { throw $taskView.Message }
        Write-Host "`n模式：$(if ($taskView.Online) { '在线，服务确认后生效' } else { '离线，保存后下次启动生效' })"
        $taskIndex = 0
        foreach ($taskGroup in $taskView.Groups) {
            $taskIndex++
            Write-Host "$taskIndex. 群 $($taskGroup.ID)；启用=$($taskGroup.Enabled)"
            foreach ($taskUser in $taskGroup.Members) { Write-Host "   $($taskUser.ID)；启用=$($taskUser.Enabled)" }
        }
        Write-Host '1 刷新；2 添加成员；3 启用成员；4 停用成员（保留资料）；5 查看任务；6 停止任务；0 退出'
        $taskAction = Read-SetupText '请选择'
        if ($taskAction -eq '0') { break }
        if ($taskAction -eq '1') { continue }
        if ($taskAction -notin @('2','3','4','5','6')) { throw '无效选项。' }
        $taskGroupIndex = (Read-SetupCount '群编号' 1 @($taskView.Groups).Count) - 1
        $taskRequest = @{ Version=$taskView.Version; Group=$taskView.Groups[$taskGroupIndex].ID }
        switch ($taskAction) {
            '2' { $taskRequest.Action='add' }
            '3' { $taskRequest.Action='enable' }
            '4' { $taskRequest.Action='disable' }
            '5' { $taskRequest.Action='tasks' }
            '6' { $taskRequest.Action='stop'; $taskRequest.Task=Read-SetupText '任务编号' }
        }
        if ($taskAction -in @('2','3','4')) {
            $taskRequest.User=Read-SetupText '成员稳定用户 ID'
            Assert-SetupID $taskRequest.User
            Write-Host "群：$($taskRequest.Group)；成员：$($taskRequest.User)；操作：$($taskRequest.Action)"
            if ($taskAction -eq '4') { Write-Host '将撤销新请求、取消排队及运行任务、作废旧确认与审批；资料保留。' }
            if ((Read-SetupText '确认操作请输入 YES') -cne 'YES') { continue }
        }
        $taskResult=Invoke-MemberManagement $taskRequest
        if ($taskResult.Message) { Write-Host $taskResult.Message }
        if ($taskResult.Tasks) { Write-Host $taskResult.Tasks }
        if (-not $taskResult.Success) { Write-Host '操作未完成。请刷新核对。' }
    } catch {
        Write-Host $_.Exception.Message
        if ((Read-SetupText '回车重试，输入 0 退出') -eq '0') { break }
    }
}
