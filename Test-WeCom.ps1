$ErrorActionPreference = 'Stop'
$taskBinary=Join-Path $PSScriptRoot 'bin\group-assistant.exe'
if (-not (Test-Path -LiteralPath $taskBinary -PathType Leaf)) { throw 'Build group-assistant.exe first.' }
$taskBotID=Read-Host 'Bot ID'
$taskSecureSecret=Read-Host 'Secret (hidden)' -AsSecureString
$taskPreviousID=[Environment]::GetEnvironmentVariable('CC_GROUP_BOT_ID','Process')
$taskPreviousSecret=[Environment]::GetEnvironmentVariable('CC_GROUP_BOT_SECRET','Process')
$taskPlainSecret=$null
try {
 $taskPlainSecret=[Net.NetworkCredential]::new('',$taskSecureSecret).Password
 [Environment]::SetEnvironmentVariable('CC_GROUP_BOT_ID',$taskBotID.Trim(),'Process')
 [Environment]::SetEnvironmentVariable('CC_GROUP_BOT_SECRET',$taskPlainSecret,'Process')
 & $taskBinary -probe-wecom
 if ($LASTEXITCODE -ne 0) {throw 'WeCom connection test exited with an error.'}
} finally {
 [Environment]::SetEnvironmentVariable('CC_GROUP_BOT_ID',$taskPreviousID,'Process')
 [Environment]::SetEnvironmentVariable('CC_GROUP_BOT_SECRET',$taskPreviousSecret,'Process')
 $taskPlainSecret=$null
 $taskSecureSecret.Dispose()
}
