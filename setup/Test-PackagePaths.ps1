param([Parameter(Mandatory=$true)][string]$Payload)
$ErrorActionPreference = 'Stop'
. (Join-Path $Payload 'Operator-Paths.ps1')
$taskBase = Get-OperatorBase $Payload
if ($taskBase.Owner -cne 'YOUR_USER_ID' -or @($taskBase.Groups).Count -ne 1 -or $taskBase.Groups[0] -cne 'YOUR_GROUP_ID') { throw 'Personal identifiers in payload.' }
if ($taskBase.CodexCommand -or $taskBase.PythonCommand -or $taskBase.NodeCommand -or $taskBase.NpmCLI) { throw 'Personal executable paths in payload.' }
if ($taskBase.BoundaryVerified -ne $false -or @($taskBase.ToolRoots).Count -ne 0 -or @($taskBase.AllowedHosts).Count -ne 0) { throw 'Unexpected granted capability in payload.' }
$taskUserRoot = Join-Path ([Environment]::GetFolderPath('LocalApplicationData')) 'Tongxu'
if ($taskBase.StateDir -ine (Join-Path $taskUserRoot 'state') -or $taskBase.CodexHome -ine (Join-Path $taskUserRoot 'runtime\codex-home')) { throw 'Installed user-data layout mismatch.' }
Write-Output 'PASS: Windows PowerShell 5.1 loads packaged scripts under current policy; sanitized config and installed per-user paths verified. No configuration changed.'
