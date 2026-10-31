param([string]$Go = 'D:\Go\bin\go.exe', [string]$CacheDirectory = (Join-Path $PSScriptRoot 'build-cache'))
$ErrorActionPreference = 'Stop'
$sourceDir = Join-Path $PSScriptRoot 'backend'
$cacheDir = [IO.Path]::GetFullPath($CacheDirectory)
if (-not (Test-Path -LiteralPath $Go -PathType Leaf)) { throw 'Set -Go to an existing Go executable; this script does not install tools.' }
$taskEnvironment = @{
 GOTOOLCHAIN = 'local'
 GOMODCACHE = Join-Path $cacheDir 'modules'
 GOCACHE = Join-Path $cacheDir 'go-build'
 GOPATH = Join-Path $cacheDir 'gopath'
 GOTMPDIR = Join-Path $cacheDir 'tmp'
 TEMP = Join-Path $cacheDir 'tmp'
 TMP = Join-Path $cacheDir 'tmp'
 GOPROXY = 'off'
 CGO_ENABLED = '0'
}
$originalEnvironment = @{}
foreach ($key in $taskEnvironment.Keys) { $originalEnvironment[$key] = [Environment]::GetEnvironmentVariable($key,'Process') }
Push-Location -LiteralPath $sourceDir
try {
 New-Item -ItemType Directory -Path (Join-Path $cacheDir 'tmp'),(Join-Path $PSScriptRoot 'bin') -Force | Out-Null
 foreach ($key in $taskEnvironment.Keys) { [Environment]::SetEnvironmentVariable($key,$taskEnvironment[$key],'Process') }
 & $Go test -p 2 ./groupassistant ./agent/codex ./cmd/group-assistant ./platform/wecom -count=1
 if ($LASTEXITCODE -ne 0) { throw 'Group-assistant regression tests failed.' }
 & $Go test ./core -run TestCUJ -count=1
 if ($LASTEXITCODE -ne 0) { throw 'Core user-journey tests failed.' }
 & $Go vet ./groupassistant ./agent/codex ./cmd/group-assistant ./platform/wecom
 if ($LASTEXITCODE -ne 0) { throw 'Static checks failed.' }
 & $Go build -o (Join-Path $PSScriptRoot 'bin\group-assistant.exe') ./cmd/group-assistant
 if ($LASTEXITCODE -ne 0) { throw 'Build failed.' }
 & (Join-Path $PSScriptRoot 'validation\Test-OperatorSetup.ps1') -ProjectRoot $PSScriptRoot -Go $Go
 & (Join-Path $PSScriptRoot 'validation\Test-OperatorPaths.ps1') -ProjectRoot $PSScriptRoot
 & (Join-Path $PSScriptRoot 'validation\Test-LiveManagement.ps1') -ProjectRoot $PSScriptRoot
 Write-Output 'Built bin\group-assistant.exe. No service started. Start-GroupAssistant.ps1 now starts formal service directly; live acceptance remains required.'
} finally {
 foreach ($key in $originalEnvironment.Keys) { [Environment]::SetEnvironmentVariable($key,$originalEnvironment[$key],'Process') }
 Pop-Location
}
