param([switch]$StatusOnly, [switch]$ChooseCodex)
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'Operator-Setup.ps1')
$taskConfigPath = Join-Path $PSScriptRoot 'config.group-assistant.json'
$taskBinary = Join-Path $PSScriptRoot 'bin\group-assistant.exe'
if (-not (Test-Path -LiteralPath $taskBinary -PathType Leaf)) { throw 'Build group-assistant.exe first.' }
$taskBase = Get-OperatorBase $PSScriptRoot
$taskRuntime = Get-OperatorRuntime $PSScriptRoot
$taskSetup = Read-OperatorSetup (Join-Path $taskRuntime 'operator-setup.json') $taskBase
$taskConfig = Merge-OperatorPaths $taskBase $taskSetup
$taskConfig.CodexCommand = Select-NativeCodex $taskConfig.CodexCommand -Choose:$ChooseCodex
if ($null -ne $taskSetup) {
    $taskSetup = $taskSetup | ConvertTo-Json -Depth 20 | ConvertFrom-Json
    if ($null -eq $taskSetup.PSObject.Properties['Paths']) { $taskSetup | Add-Member -NotePropertyName Paths -NotePropertyValue ([pscustomobject]@{}) }
    $taskSetup.Paths | Add-Member -NotePropertyName CodexCommand -NotePropertyValue $taskConfig.CodexCommand -Force
}
if ($null -ne $taskSetup) { Assert-OperatorPaths $taskBase $taskSetup }
if (-not (Test-NativeCodexPath $taskConfig.CodexCommand)) { throw 'Codex path unavailable. Select an existing native executable in the configuration menu first; no login started.' }
$taskCheckPath = Join-Path $taskRuntime ('.login-check-' + [guid]::NewGuid().ToString('N') + '.json')
try {
    New-Item -ItemType Directory -Path $taskRuntime -Force | Out-Null
    Write-PrivateJson $taskCheckPath $taskConfig
    & $taskBinary -config $taskCheckPath -check
    if ($LASTEXITCODE -ne 0) { throw 'Configuration check failed; no login started.' }
} finally { if (Test-Path -LiteralPath $taskCheckPath) { Remove-Item -LiteralPath $taskCheckPath } }
$taskLoginTemp = Join-Path $taskRuntime 'login-temp'
New-Item -ItemType Directory -Path $taskConfig.CodexHome,$taskLoginTemp -Force | Out-Null
$taskStart = [Diagnostics.ProcessStartInfo]::new()
$taskStart.FileName = $taskConfig.CodexCommand
$taskStart.UseShellExecute = $false
$taskStart.WorkingDirectory = $PSScriptRoot
$taskStart.Arguments = '-c windows.sandbox=mxc -c cli_auth_credentials_store="file" login'
if ($StatusOnly) { $taskStart.Arguments += ' status' } else { $taskStart.Arguments += ' --device-auth' }
$taskStart.EnvironmentVariables.Clear()
foreach ($taskKey in @(
    'PATH','PATHEXT','SYSTEMROOT','SYSTEMDRIVE','WINDIR','COMSPEC','USERPROFILE',
    'LOCALAPPDATA','APPDATA','PROGRAMFILES','PROGRAMFILES(X86)','PROGRAMW6432',
    'PROGRAMDATA','HOMEDRIVE','HOMEPATH','NO_PROXY','OS','NUMBER_OF_PROCESSORS','PROCESSOR_ARCHITECTURE'
)) {
    $taskValue = [Environment]::GetEnvironmentVariable($taskKey, 'Process')
    if ($null -ne $taskValue) { $taskStart.EnvironmentVariables[$taskKey] = $taskValue }
}
$taskStart.EnvironmentVariables['CODEX_HOME'] = $taskConfig.CodexHome
$taskStart.EnvironmentVariables['TEMP'] = $taskLoginTemp
$taskStart.EnvironmentVariables['TMP'] = $taskLoginTemp
Write-Output "Dedicated Codex home: $($taskConfig.CodexHome)"
Write-Output 'This only manages the dedicated login. It does not start group work or change system environment variables.'
$taskProcess = [Diagnostics.Process]::new()
$taskProcess.StartInfo = $taskStart
try {
    if (-not $taskProcess.Start()) { throw 'Could not start existing Codex.' }
    $taskProcess.WaitForExit()
    if ($taskProcess.ExitCode -ne 0) { throw 'Codex login/status failed. No group runtime was enabled.' }
} finally {
    $taskProcess.Dispose()
}
