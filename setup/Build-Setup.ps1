param([string]$OutputDirectory = (Join-Path $PSScriptRoot 'dist'), [string]$DesktopDirectory = '')
$ErrorActionPreference = 'Stop'
$taskRoot = Split-Path $PSScriptRoot -Parent
$taskCompiler = Join-Path $env:WINDIR 'Microsoft.NET\Framework64\v4.0.30319\csc.exe'
if (-not (Test-Path -LiteralPath $taskCompiler)) { throw 'Existing Windows .NET Framework compiler not found; no tools will be downloaded.' }
$taskOutput = [IO.Path]::GetFullPath($OutputDirectory)
$taskWork = Join-Path $PSScriptRoot ('build-' + [guid]::NewGuid().ToString('N'))
$taskPayload = Join-Path $taskWork 'payload'
New-Item -ItemType Directory -Path $taskPayload,$taskOutput -Force | Out-Null
$taskFiles = @(
 'Desktop-Operator.ps1','Operator-Setup.ps1','Operator-Paths.ps1','Runtime-Paths.ps1',
 'Start-GroupAssistant.ps1','Login-Codex.ps1','Configure-GroupAssistant.ps1','Manage-GroupAssistant.ps1',
 'Install-Update.ps1',
 'bin\group-assistant.exe','bin\desktop\tongxu.exe','bin\desktop\tongxu.dll',
 'bin\desktop\tongxu.deps.json','bin\desktop\tongxu.runtimeconfig.json'
)
foreach ($taskRelative in $taskFiles) {
 $taskSource = Join-Path $taskRoot $taskRelative
 if ($DesktopDirectory -and $taskRelative.StartsWith('bin\desktop\')) { $taskSource = Join-Path $DesktopDirectory ([IO.Path]::GetFileName($taskRelative)) }
 if (-not (Test-Path -LiteralPath $taskSource -PathType Leaf)) { throw "Missing release file: $taskRelative" }
 $taskDestination = Join-Path $taskPayload $taskRelative
 New-Item -ItemType Directory -Path (Split-Path $taskDestination -Parent) -Force | Out-Null
 Copy-Item -LiteralPath $taskSource -Destination $taskDestination
}
# Explicit public template, never the current deployment's config or runtime.
$taskTemplate = Join-Path $taskRoot 'config.group-assistant.example.json'
if (-not (Test-Path -LiteralPath $taskTemplate -PathType Leaf)) { $taskTemplate = Join-Path (Split-Path $taskRoot -Parent) 'TONGXU-public\config.group-assistant.example.json' }
$taskConfig = Get-Content -LiteralPath $taskTemplate -Raw -Encoding UTF8 | ConvertFrom-Json
if ($taskConfig.Owner -cne 'YOUR_USER_ID' -or @($taskConfig.Groups).Count -ne 1 -or $taskConfig.Groups[0] -cne 'YOUR_GROUP_ID') { throw 'Public configuration template is not sanitized.' }
$taskConfig.CodexCommand = ''
$taskConfig.BoundaryVerified = $false
$taskConfig.ToolRoots = @(); $taskConfig.AllowedHosts = @()
foreach ($taskKey in @('PythonCommand','NodeCommand','NpmCLI')) { $taskConfig.$taskKey = '' }
$taskUTF8 = [Text.UTF8Encoding]::new($false)
[IO.File]::WriteAllText((Join-Path $taskPayload 'config.group-assistant.json'), ($taskConfig | ConvertTo-Json -Depth 20), $taskUTF8)
[IO.File]::WriteAllText((Join-Path $taskPayload 'installed-layout.json'), '{"Version":1}', $taskUTF8)
$taskManifest = [ordered]@{}
foreach ($taskFile in Get-ChildItem -LiteralPath $taskPayload -Recurse -File | Sort-Object FullName) {
 $taskRelative = $taskFile.FullName.Substring($taskPayload.Length + 1).Replace('\','/')
 $taskManifest[$taskRelative] = (Get-FileHash -LiteralPath $taskFile.FullName -Algorithm SHA256).Hash.ToLowerInvariant()
}
$taskManifestPath = Join-Path $taskWork 'files.json'
[IO.File]::WriteAllText($taskManifestPath, ($taskManifest | ConvertTo-Json), $taskUTF8)
$taskVersion = [Reflection.AssemblyName]::GetAssemblyName((Join-Path $taskPayload 'bin\desktop\tongxu.dll')).Version
$taskAppVersion = '{0}.{1}.{2}' -f $taskVersion.Major,$taskVersion.Minor,$taskVersion.Build
$taskReleaseManifest = [ordered]@{Version=$taskAppVersion;SetupProtocol=1;RuntimeMajor=10;Files=$taskManifest}
[IO.File]::WriteAllText((Join-Path $taskPayload 'tongxu-package.json'), ($taskReleaseManifest | ConvertTo-Json -Depth 8), $taskUTF8)
Add-Type -AssemblyName System.IO.Compression.FileSystem
$taskZip = Join-Path $taskOutput ('Tongxu-' + $taskAppVersion + '-win-x64.zip')
if (Test-Path -LiteralPath $taskZip) { throw 'Release bundle already exists; choose a new output directory to avoid overwriting a prepared release.' }
[IO.Compression.ZipFile]::CreateFromDirectory($taskPayload, $taskZip, [IO.Compression.CompressionLevel]::Optimal, $false)
$taskExe = Join-Path $taskOutput 'TONGXU-Setup-1.0.0.exe'
& $taskCompiler /nologo /target:winexe /platform:x64 /optimize+ "/out:$taskExe" "/win32manifest:$(Join-Path $PSScriptRoot 'setup.manifest')" "/win32icon:$(Join-Path $taskRoot 'desktop\Assets\tongxu.ico')" /reference:System.dll /reference:System.Core.dll /reference:System.Net.Http.dll /reference:System.Drawing.dll /reference:System.Windows.Forms.dll /reference:System.IO.Compression.dll /reference:System.IO.Compression.FileSystem.dll /reference:System.Web.Extensions.dll "/resource:$(Join-Path $taskRoot 'desktop\Assets\tongxu.ico'),tongxu.ico" (Join-Path $PSScriptRoot 'Setup.cs') (Join-Path $PSScriptRoot 'OnlineRelease.cs')
if ($LASTEXITCODE -ne 0) { throw 'Setup compilation failed.' }
Copy-Item -LiteralPath $taskManifestPath -Destination (Join-Path $taskOutput 'payload-files.json') -Force
$taskHash = Get-FileHash -LiteralPath $taskExe -Algorithm SHA256
[IO.File]::WriteAllText(($taskExe + '.sha256'), ($taskHash.Hash.ToLowerInvariant() + '  ' + [IO.Path]::GetFileName($taskExe) + [Environment]::NewLine), $taskUTF8)
$taskZipHash = Get-FileHash -LiteralPath $taskZip -Algorithm SHA256
[IO.File]::WriteAllText(($taskZip + '.sha256'), ($taskZipHash.Hash.ToLowerInvariant() + '  ' + [IO.Path]::GetFileName($taskZip) + [Environment]::NewLine), $taskUTF8)
Write-Output "Built online bootstrapper $taskExe and release bundle $taskZip. Nothing uploaded, downloaded or installed. Build evidence: $taskWork"
