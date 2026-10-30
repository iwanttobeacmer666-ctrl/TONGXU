param(
    [string]$Own,
    [string]$Public,
    [string]$Foreign,
    [string]$Store,
    [string]$AuthDir,
    [string]$Tool,
    [string]$External
)
$ErrorActionPreference = 'Stop'
$script:taskChecks = 0

function Assert-Readable([string]$Label, [string]$Path) {
    if ([IO.File]::ReadAllText($Path) -ne 'fixture') { throw "$Label read returned unexpected data" }
    Write-Output "PASS $Label read"
    $script:taskChecks++
}

function Assert-ReadDenied([string]$Label, [string]$Path) {
    $taskDenied = $false
    try { $null = [IO.File]::ReadAllText($Path) } catch { $taskDenied = $true }
    if (-not $taskDenied) { throw "$Label read escaped the boundary" }
    Write-Output "PASS $Label read denied"
    $script:taskChecks++
}

function Assert-WriteDenied([string]$Label, [string]$Path) {
    $taskDenied = $false
    try { [IO.File]::WriteAllText($Path, 'unauthorized') } catch { $taskDenied = $true }
    if (-not $taskDenied) { throw "$Label write escaped the boundary" }
    Write-Output "PASS $Label write denied"
    $script:taskChecks++
}

Assert-Readable 'own' $Own
[IO.File]::WriteAllText($Own, 'updated')
if ([IO.File]::ReadAllText($Own) -ne 'updated') { throw 'own write failed' }
Write-Output 'PASS own write'
$script:taskChecks++
Assert-Readable 'public' $Public
Assert-WriteDenied 'public' $Public
Assert-WriteDenied 'public new file' (Join-Path (Split-Path -Parent $Public) 'new.txt')
Assert-Readable 'tool' $Tool
Assert-WriteDenied 'tool' $Tool
foreach ($taskItem in @(
    @{Label='other member'; Path=$Foreign},
    @{Label='persistent originals'; Path=$Store},
    @{Label='authentication directory'; Path=$AuthDir},
    @{Label='outside workspace'; Path=$External}
)) {
    Assert-ReadDenied $taskItem.Label $taskItem.Path
    Assert-WriteDenied $taskItem.Label $taskItem.Path
}
$taskPublicDeleteDenied = $false
try { [IO.File]::Delete($Public) } catch { $taskPublicDeleteDenied = $true }
if (-not $taskPublicDeleteDenied) { throw 'public file deletion escaped the boundary' }
Write-Output 'PASS public delete denied'
$script:taskChecks++

foreach ($taskRegistry in @(
    @{Label='user environment'; Root=[Microsoft.Win32.Registry]::CurrentUser; Path='Environment'},
    @{Label='system environment'; Root=[Microsoft.Win32.Registry]::LocalMachine; Path='SYSTEM\CurrentControlSet\Control\Session Manager\Environment'}
)) {
    $taskWriteHandleDenied = $false
    $taskKey = $null
    try {
        $taskKey = $taskRegistry.Root.OpenSubKey($taskRegistry.Path, $true)
        if ($null -eq $taskKey) { throw 'expected registry key unavailable' }
    } catch { $taskWriteHandleDenied = $true } finally {
        if ($null -ne $taskKey) { $taskKey.Dispose() }
    }
    if (-not $taskWriteHandleDenied) { throw "$($taskRegistry.Label) writable registry handle unexpectedly allowed" }
    Write-Output "PASS $($taskRegistry.Label) write handle denied (no registry writes attempted)"
    $script:taskChecks++
}
Write-Output "CC_GROUP_BOUNDARY_OK checks=$script:taskChecks"
