param([Parameter(Mandatory=$true)][string]$SourceRoot)
$ErrorActionPreference = 'Stop'
# Explicit, same-user migration preparation. Does not install, delete, move,
# connect, or copy login files. Existing data paths remain authoritative.
. (Join-Path $PSScriptRoot 'Operator-Setup.ps1')
$SourceRoot = [IO.Path]::GetFullPath($SourceRoot)
if (Test-InstalledLayout $SourceRoot) { throw 'Source must be an existing development/portable deployment.' }
$taskSourceRuntime = Join-Path $SourceRoot 'runtime'
$taskDestination = Join-Path ([Environment]::GetFolderPath('LocalApplicationData')) 'Tongxu\runtime'
if (Test-Path -LiteralPath $taskDestination) { throw 'Destination already exists; nothing was changed. Review and merge manually.' }
$taskLocks = [Collections.Generic.List[IDisposable]]::new()
try {
    foreach ($taskName in @('launcher.lock','operator.lock','service.lock')) {
        $taskLocks.Add([IO.File]::Open((Join-Path $taskSourceRuntime $taskName),'OpenOrCreate','ReadWrite','None'))
    }
    $taskBase = Get-OperatorBase $SourceRoot
    $taskSetup = Read-OperatorSetup (Join-Path $taskSourceRuntime 'operator-setup.json') $taskBase
    if ($null -eq $taskSetup) { throw 'No existing operator settings to migrate.' }
    $taskEffective = Merge-OperatorPaths $taskBase $taskSetup
    $taskPaths = [ordered]@{}
    foreach ($taskKey in $script:OperatorPathKeys) { $taskPaths[$taskKey] = $taskEffective.$taskKey }
    $taskSetup | Add-Member -NotePropertyName Paths -NotePropertyValue ([pscustomobject]$taskPaths) -Force
    # Prepare adjacent staging first; rename makes the new layout visible only
    # after both settings files have been copied successfully.
    $taskStage = $taskDestination + '.prepare-' + [guid]::NewGuid().ToString('N')
    New-Item -ItemType Directory -Path $taskStage -Force | Out-Null
    Write-PrivateJson (Join-Path $taskStage 'operator-setup.json') $taskSetup
    $taskAppearance = Join-Path $taskSourceRuntime 'desktop-settings.json'
    if (Test-Path -LiteralPath $taskAppearance -PathType Leaf) { Copy-Item -LiteralPath $taskAppearance -Destination (Join-Path $taskStage 'desktop-settings.json') }
    # No overwrite: a concurrently created destination causes a safe failure.
    [IO.Directory]::Move($taskStage,$taskDestination)
    Write-Output 'Prepared current-user settings. Original data and Codex login paths remain in use; old files were not removed. The installed and old deployments must not run concurrently.'
} finally { foreach ($taskLock in $taskLocks) { $taskLock.Dispose() } }
