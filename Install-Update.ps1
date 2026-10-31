param(
    [Parameter(Mandatory=$true)][string]$ProjectRoot,
    [Parameter(Mandatory=$true)][string]$Runtime,
    [Parameter(Mandatory=$true)][string]$Package,
    [Parameter(Mandatory=$true)][ValidatePattern('^[a-f0-9]{64}$')][string]$Sha256,
    [Parameter(Mandatory=$true)][ValidatePattern('^\d+\.\d+\.\d+$')][string]$Version,
    [Parameter(Mandatory=$true)][int]$ParentId,
    [Parameter(Mandatory=$true)][string]$ReadyFile
)
$ErrorActionPreference = 'Stop'
$taskLocks = [Collections.Generic.List[IDisposable]]::new()
$taskInput = $null
$taskResult = @{Success=$false;Version=$Version;Message='Update preparation failed.'}
$taskCanRestart = $false
try {
    $ProjectRoot = [IO.Path]::GetFullPath($ProjectRoot)
    $Runtime = [IO.Path]::GetFullPath($Runtime)
    . (Join-Path $ProjectRoot 'Runtime-Paths.ps1')
    if (-not (Test-InstalledLayout $ProjectRoot) -or (Get-OperatorRuntime $ProjectRoot) -ine $Runtime) { throw 'Only an installed per-user layout can update.' }
    $taskUpdates = Join-Path $Runtime 'updates'
    $Package = [IO.Path]::GetFullPath($Package)
    if ((Split-Path $Package -Parent) -ine $taskUpdates -or [IO.Path]::GetFileName($Package) -notmatch '^package-[a-f0-9]{32}\.msi$') { throw 'Invalid cached package path.' }
    # Hold a read handle that disallows replacement/writes through MSI completion.
    $taskInput = [IO.File]::Open($Package,'Open','Read','Read')
    $taskHasher = [Security.Cryptography.SHA256]::Create()
    try { $taskHash = ([BitConverter]::ToString($taskHasher.ComputeHash($taskInput))).Replace('-','').ToLowerInvariant() } finally { $taskHasher.Dispose() }
    if ($taskHash -cne $Sha256) { throw 'Installer checksum mismatch.' }
    $taskInstaller = New-Object -ComObject WindowsInstaller.Installer
    $taskDatabase = $taskInstaller.OpenDatabase($Package, 0)
    function Get-MsiProperty([string]$Name) {
        $taskView = $taskDatabase.OpenView("SELECT ``Value`` FROM ``Property`` WHERE ``Property``='$Name'")
        try { $taskView.Execute(); $taskRecord = $taskView.Fetch(); if ($null -eq $taskRecord) { return '' }; return $taskRecord.StringData(1) } finally { $taskView.Close() }
    }
    if ((Get-MsiProperty 'UpgradeCode') -ine '{A15AB6E2-CE98-48BD-B8E4-8D11F58A24D1}' -or
        (Get-MsiProperty 'ProductVersion') -cne $Version -or
        (Get-MsiProperty 'TONGXU_UPDATE_PROTOCOL') -cne '1') { throw 'MSI does not match the Tongxu update contract.' }
    [Runtime.InteropServices.Marshal]::FinalReleaseComObject($taskDatabase) | Out-Null
    [Runtime.InteropServices.Marshal]::FinalReleaseComObject($taskInstaller) | Out-Null
    # Do not disable script policy or request elevation. Tell the desktop only
    # after policy, path and checksum checks have succeeded.
    $taskParent = Get-Process -Id $ParentId -ErrorAction SilentlyContinue
    if ($null -eq $taskParent -or $taskParent.Path -ine (Join-Path $ProjectRoot 'bin\desktop\tongxu.exe')) { throw 'Unexpected parent desktop.' }
    [IO.File]::WriteAllText($ReadyFile, 'ready')
    if ($null -ne $taskParent -and -not $taskParent.WaitForExit(60000)) { throw 'Desktop did not exit; no installation performed.' }
    # A second desktop/launcher or incomplete backend cleanup blocks replacement.
    $taskDeadline = [DateTime]::UtcNow.AddMinutes(10)
    foreach ($taskName in @('desktop.lock','launcher.lock','operator.lock','service.lock')) {
        while ($true) {
            try { $taskLocks.Add([IO.File]::Open((Join-Path $Runtime $taskName),'OpenOrCreate','ReadWrite','None')); break }
            catch [IO.IOException] { if ([DateTime]::UtcNow -ge $taskDeadline) { throw 'Application still in use; no installation performed.' }; Start-Sleep -Milliseconds 250 }
        }
    }
    $taskCanRestart = $true
    $taskAssembly = Join-Path $ProjectRoot 'bin\desktop\tongxu.dll'
    $taskInstalledVersion = [Reflection.AssemblyName]::GetAssemblyName($taskAssembly).Version
    if ($taskInstalledVersion -ge ([version]($Version + '.0'))) { throw 'This version or a newer version is already installed; no installation performed.' }
    $taskLog = Join-Path $PSScriptRoot 'installer.log'
    $taskStart = [Diagnostics.ProcessStartInfo]::new()
    $taskStart.FileName = Join-Path ([Environment]::GetFolderPath('System')) 'msiexec.exe'
    $taskStart.UseShellExecute = $false
    $taskStart.CreateNoWindow = $true
    # PS 5.1 lacks ArgumentList. Windows paths cannot contain a quote; reject it
    # explicitly and quote each path rather than evaluating shell text.
    foreach ($taskPath in @($Package,$ProjectRoot,$taskLog)) { if ($taskPath.Contains('"')) { throw 'Invalid path.' } }
    $taskStart.Arguments = '/i "' + $Package + '" /passive /norestart ALLUSERS=2 MSIINSTALLPERUSER=1 TONGXU_UPDATE=1 INSTALLFOLDER="' + $ProjectRoot.TrimEnd('\') + '" /l*v "' + $taskLog + '"'
    $taskProcess = [Diagnostics.Process]::Start($taskStart)
    try { $taskProcess.WaitForExit(); $taskExit = $taskProcess.ExitCode } finally { $taskProcess.Dispose() }
    $taskResult.ExitCode = $taskExit
    $taskResult.Success = $taskExit -in @(0,3010)
    if ($taskResult.Success) {
        $taskActualVersion = [Reflection.AssemblyName]::GetAssemblyName($taskAssembly).Version
        $taskResult.Success = $taskActualVersion -eq ([version]($Version + '.0'))
        $taskResult.RestartRequired = $taskExit -eq 3010
    }
    $taskResult.Message = if ($taskResult.Success) { 'Installer completed.' } else { 'Installer failed. Check installer.log and the previous installation.' }
} catch { $taskResult.Message = $_.Exception.Message }
finally {
    if ($null -ne $taskInput) { $taskInput.Dispose() }
    foreach ($taskLock in $taskLocks) { $taskLock.Dispose() }
    try { $taskResult | ConvertTo-Json -Compress | Set-Content -LiteralPath (Join-Path $PSScriptRoot 'result.json') -Encoding UTF8 } catch { }
    try {
        $taskResultTemp = Join-Path $Runtime ('updates\install-result-' + [guid]::NewGuid().ToString('N') + '.tmp')
        [IO.File]::WriteAllText($taskResultTemp, ($taskResult | ConvertTo-Json -Compress), [Text.UTF8Encoding]::new($false))
        Move-Item -LiteralPath $taskResultTemp -Destination (Join-Path $Runtime 'updates\install-result.json') -Force
    } catch { }
}
if ($taskCanRestart) {
    $taskDesktop = Join-Path $ProjectRoot 'bin\desktop\tongxu.exe'
    if (Test-Path -LiteralPath $taskDesktop -PathType Leaf) {
        $taskRestart = [Diagnostics.ProcessStartInfo]::new()
        $taskRestart.FileName = $taskDesktop
        $taskRestart.Arguments = '--project "' + $ProjectRoot + '"'
        $taskRestart.WorkingDirectory = $ProjectRoot
        $taskRestart.UseShellExecute = $false
        [Diagnostics.Process]::Start($taskRestart) | Out-Null
    }
}
if (-not $taskResult.Success) { exit 1 }
