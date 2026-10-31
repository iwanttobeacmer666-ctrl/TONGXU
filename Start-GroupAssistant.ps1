param([switch]$Configure, [switch]$ChooseCodex)
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'Operator-Setup.ps1')
$taskBinary = Join-Path $PSScriptRoot 'bin\group-assistant.exe'
$taskDirectory = Get-OperatorRuntime $PSScriptRoot
$taskSetupPath = Join-Path $taskDirectory 'operator-setup.json'
$taskLock = $null
try {
    if (-not (Test-Path -LiteralPath $taskBinary -PathType Leaf)) { throw '请先使用已有 build.ps1 构建。' }
    $taskRunning = @(Get-Process -Name 'group-assistant' -ErrorAction SilentlyContinue | Where-Object { $_.Path -eq $taskBinary })
    if ($taskRunning.Count -gt 0) { throw '同序已在运行。配置修改请先在旧窗口 Ctrl+C 停止；在线成员管理请使用 Manage-GroupAssistant.ps1。' }
    New-Item -ItemType Directory -Path $taskDirectory -Force | Out-Null
    try { $taskLock = [IO.File]::Open((Join-Path $taskDirectory 'launcher.lock'), 'OpenOrCreate', 'ReadWrite', 'None') }
    catch { throw '另一个终端正在配置或运行同序。请先退出该终端。' }
    $taskBase = Get-OperatorBase $PSScriptRoot
    $taskSnapshot = Read-OperatorSnapshot $taskSetupPath $taskBase
    $taskSetup = $taskSnapshot.Setup
    $taskSetupVersion = $taskSnapshot.Version
    if ($null -eq $taskSetup) {
        if ($Configure) { Write-Host '首次配置：仅保存，不连接群。' }
        else { Write-Host '首次配置：确认 SAVE 后将直接启动正式服务；取消则不连接群。' }
        $taskDraft = New-OperatorSetup $taskBase
        if (-not (Save-OperatorSetup $taskSetupPath $taskDraft $taskBase $taskBinary -ExpectedVersion $taskSetupVersion)) { return }
        $taskSetup = $taskDraft
    }
    if (-not $Configure) {
        # Service reloads the latest settings under the configuration lock.
        # No startup menu, automatic restart or retry after the process exits.
        Start-OperatorService $taskBinary $taskBase $taskSetup $taskDirectory -Mode Formal -ChooseCodex:$ChooseCodex
        return
    }
    while ($true) {
        $taskSnapshot = Read-OperatorSnapshot $taskSetupPath $taskBase
        $taskSetup = $taskSnapshot.Setup
        $taskSetupVersion = $taskSnapshot.Version
        Write-Host "`n2 配置管理；3 查看配置摘要；0 退出（此入口不会启动服务）"
        try {
            $taskChoice = Read-SetupText '请选择'
            switch ($taskChoice) {
                '0' { return }
                '2' {
                    $taskDraft = Edit-OperatorSetup $taskSetup $taskBase
                    if ($null -ne $taskDraft -and (Save-OperatorSetup $taskSetupPath $taskDraft $taskBase $taskBinary -ExpectedVersion $taskSetupVersion)) { $taskSetup = $taskDraft }
                }
                '3' { Show-OperatorSetup $taskSetup $taskBase }
                default { Write-Host '无效选项。' }
            }
        } catch { Write-Host $_.Exception.Message }
    }
} catch {
    throw ($_.Exception.Message + ' 配置入口：Configure-GroupAssistant.ps1；未自动修复或重新启动。')
} finally { if ($null -ne $taskLock) { $taskLock.Dispose() } }
