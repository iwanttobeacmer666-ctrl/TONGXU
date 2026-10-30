param([string]$DotNet='D:\dotnet\dotnet.exe')
$ErrorActionPreference='Stop'
if(-not(Test-Path -LiteralPath $DotNet -PathType Leaf)){throw '请指定本机已有 .NET SDK；此脚本不安装工具。'}
$taskProject=Join-Path $PSScriptRoot 'desktop\tongxu.csproj'
$taskCache=Join-Path $PSScriptRoot 'build-cache\desktop'
$taskVariables=@{DOTNET_CLI_HOME=$taskCache;DOTNET_SKIP_FIRST_TIME_EXPERIENCE='1';DOTNET_ADD_GLOBAL_TOOLS_TO_PATH='false';DOTNET_GENERATE_ASPNET_CERTIFICATE='false';DOTNET_CLI_WORKLOAD_UPDATE_NOTIFY_DISABLE='true';DOTNET_CLI_TELEMETRY_OPTOUT='1';DOTNET_NOLOGO='1';NUGET_PACKAGES=(Join-Path $taskCache 'packages')}
$taskSaved=@{}
try{
 foreach($taskKey in $taskVariables.Keys){$taskSaved[$taskKey]=[Environment]::GetEnvironmentVariable($taskKey,'Process');[Environment]::SetEnvironmentVariable($taskKey,$taskVariables[$taskKey],'Process')}
 & $DotNet restore $taskProject --configfile (Join-Path $PSScriptRoot 'desktop\NuGet.Config') --ignore-failed-sources
 if($LASTEXITCODE -ne 0){throw '本机框架引用不足，停止构建；未从网络获取依赖。'}
 & $DotNet build $taskProject --no-restore -c Release -o (Join-Path $PSScriptRoot 'bin\desktop')
 if($LASTEXITCODE -ne 0){throw '桌面程序构建失败。'}
 Write-Output 'Built bin\desktop\tongxu.exe. No service started or dependency downloaded.'
}finally{foreach($taskKey in $taskSaved.Keys){[Environment]::SetEnvironmentVariable($taskKey,$taskSaved[$taskKey],'Process')}}
