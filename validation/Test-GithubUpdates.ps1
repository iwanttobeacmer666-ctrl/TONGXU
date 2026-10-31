param([string]$ProjectRoot = (Split-Path $PSScriptRoot -Parent),[string]$DotNet='D:\dotnet\dotnet.exe')
$ErrorActionPreference='Stop'
if (-not (Test-Path -LiteralPath $DotNet -PathType Leaf)) { throw 'Use an existing .NET SDK; this test does not install tools.' }
$taskCache = Join-Path $ProjectRoot 'build-cache\desktop'
$taskVariables = @{DOTNET_CLI_HOME=$taskCache;DOTNET_SKIP_FIRST_TIME_EXPERIENCE='1';DOTNET_ADD_GLOBAL_TOOLS_TO_PATH='false';DOTNET_GENERATE_ASPNET_CERTIFICATE='false';DOTNET_CLI_TELEMETRY_OPTOUT='1';DOTNET_CLI_WORKLOAD_UPDATE_NOTIFY_DISABLE='true';NUGET_PACKAGES=(Join-Path $taskCache 'packages')}
$taskSaved = @{}
try {
    foreach ($taskKey in $taskVariables.Keys) { $taskSaved[$taskKey]=[Environment]::GetEnvironmentVariable($taskKey,'Process'); [Environment]::SetEnvironmentVariable($taskKey,$taskVariables[$taskKey],'Process') }
    $taskProject = Join-Path $ProjectRoot 'validation\update-tests\UpdateTests.csproj'
    & $DotNet restore $taskProject --configfile (Join-Path $ProjectRoot 'desktop\NuGet.Config')
    if ($LASTEXITCODE -ne 0) { throw 'Offline restore failed; no dependency installation allowed.' }
    $taskEvidence = Join-Path $ProjectRoot ('validation\github-updates\core-' + [guid]::NewGuid().ToString('N'))
    & $DotNet run --project $taskProject --no-restore -- $taskEvidence
    if ($LASTEXITCODE -ne 0) { throw 'Update core tests failed.' }
    & (Join-Path $ProjectRoot 'validation\Test-UpdatePaths.ps1') -ProjectRoot $ProjectRoot
} finally { foreach ($taskKey in $taskSaved.Keys) { [Environment]::SetEnvironmentVariable($taskKey,$taskSaved[$taskKey],'Process') } }
