param([string]$ProjectRoot = (Split-Path $PSScriptRoot -Parent))
$ErrorActionPreference = 'Stop'
. (Join-Path $ProjectRoot 'Operator-Setup.ps1')
$taskFixture = Join-Path $ProjectRoot ('validation\github-updates\paths-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $taskFixture -Force | Out-Null
$taskChecks = 0
function Assert-UpdatePath($Condition,[string]$Message) { if (-not $Condition) { throw $Message }; $script:taskChecks++ }
Assert-UpdatePath ((Get-OperatorRuntime $taskFixture) -eq (Join-Path $taskFixture 'runtime')) 'Legacy runtime changed.'
@{Version=1} | ConvertTo-Json | Set-Content -LiteralPath (Join-Path $taskFixture 'installed-layout.json') -Encoding UTF8
$taskExpected = Join-Path ([Environment]::GetFolderPath('LocalApplicationData')) 'Tongxu\runtime'
Assert-UpdatePath ((Get-OperatorRuntime $taskFixture) -eq $taskExpected) 'Installed runtime not separated.'
# Synthetic defaults only. Never read production credentials or change user data.
@{Owner='fixture';CodexHome=(Join-Path $taskFixture 'runtime\codex-home');DataDir=(Join-Path $taskFixture 'old\files');WorkDir=(Join-Path $taskFixture 'old\tmp');SoftwareDir=(Join-Path $taskFixture 'old\software');StateDir=(Join-Path $taskFixture 'old\state');ToolRoots=@();CodexCommand='D:\fixture\codex.exe'} | ConvertTo-Json | Set-Content -LiteralPath (Join-Path $taskFixture 'config.group-assistant.json') -Encoding UTF8
$taskBase = Get-OperatorBase $taskFixture
Assert-UpdatePath ($taskBase.CodexHome -eq (Join-Path $taskExpected 'codex-home')) 'Installed auth default not separated.'
Assert-UpdatePath ($taskBase.DataDir -eq (Join-Path (Split-Path $taskExpected -Parent) 'files')) 'Installed file default not separated.'
$taskCustom = Join-Path $taskFixture 'custom-files'
$taskMerged = Merge-OperatorPaths $taskBase ([pscustomobject]@{Paths=[pscustomobject]@{DataDir=$taskCustom}})
Assert-UpdatePath ($taskMerged.DataDir -eq $taskCustom) 'Saved custom path was replaced.'
@{Version=2} | ConvertTo-Json | Set-Content -LiteralPath (Join-Path $taskFixture 'installed-layout.json') -Encoding UTF8
$taskRejected = $false
try { Get-OperatorRuntime $taskFixture | Out-Null } catch { $taskRejected = $true }
Assert-UpdatePath $taskRejected 'Unsupported layout accepted.'
foreach ($taskScript in @('Runtime-Paths.ps1','Install-Update.ps1','Prepare-InstalledData.ps1','Operator-Paths.ps1','Desktop-Operator.ps1','Start-GroupAssistant.ps1','Manage-GroupAssistant.ps1','Login-Codex.ps1')) {
    $taskTokens = $null; $taskErrors = $null
    [Management.Automation.Language.Parser]::ParseFile((Join-Path $ProjectRoot $taskScript),[ref]$taskTokens,[ref]$taskErrors) | Out-Null
    Assert-UpdatePath ($taskErrors.Count -eq 0) ('PowerShell parse failed: '+$taskScript)
}
Write-Output "PASS: $taskChecks layout/PowerShell checks. No production data, migration or installation changed."
