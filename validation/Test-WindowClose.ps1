param(
    [string]$DesktopDirectory = (Join-Path $PSScriptRoot '..\setup\desktop-check\close-fix'),
    [string]$BackendFile = ''
)
$ErrorActionPreference = 'Stop'
$taskRoot = Split-Path $PSScriptRoot -Parent
$taskEvidence = Join-Path $taskRoot ('setup\verification-close-' + [guid]::NewGuid().ToString('N'))
$taskFixture = Join-Path $taskEvidence 'fixture'
$taskBin = Join-Path $taskFixture 'bin'
New-Item -ItemType Directory -Path $taskBin -Force | Out-Null
$taskSource = @'
using System;
using System.IO;
using System.Threading;
class Broker {
    static void Main() {
        var dir = AppDomain.CurrentDomain.BaseDirectory;
        File.AppendAllText(Path.Combine(dir, "requests.txt"), Console.In.ReadToEnd() + "\n");
        var mode = File.ReadAllText(Path.Combine(dir, "mode.txt"));
        if (mode == "slow") Thread.Sleep(30000);
        if (mode == "missing") return;
        if (mode == "invalid-json") { Console.Write("not-json"); return; }
        if (mode == "unconfigured") { Console.Write("{\"Success\":false,\"Message\":\"Synthetic first-run configuration missing\"}"); return; }
        Console.Write("{\"Success\":true,\"Online\":" + (mode == "online" ? "true" : "false") + "}");
    }
}
'@
$taskSourcePath = Join-Path $taskEvidence 'Broker.cs'
[IO.File]::WriteAllText($taskSourcePath, $taskSource, [Text.UTF8Encoding]::new($false))
$taskCompiler = Join-Path $env:WINDIR 'Microsoft.NET\Framework64\v4.0.30319\csc.exe'
& $taskCompiler /nologo /target:exe ("/out:" + (Join-Path $taskBin 'group-assistant.exe')) $taskSourcePath
if ($LASTEXITCODE -ne 0) { throw 'Fixture compilation failed' }
$taskProject = Join-Path $PSScriptRoot 'close-tests\CloseTests.csproj'
# Only installed framework references are required; NuGet.Config has no sources.
& 'D:\dotnet\dotnet.exe' restore $taskProject --configfile (Join-Path $taskRoot 'desktop\NuGet.Config') "-p:DesktopDirectory=$DesktopDirectory"
if ($LASTEXITCODE -ne 0) { throw 'Offline close test restore failed' }
$taskOutput = Join-Path $taskEvidence 'tests'
& 'D:\dotnet\dotnet.exe' build $taskProject --no-restore -c Release "-p:DesktopDirectory=$DesktopDirectory" -o $taskOutput
if ($LASTEXITCODE -ne 0) { throw 'Close test build failed' }
$taskArguments = @((Join-Path $taskOutput 'CloseTests.dll'), $taskFixture)
if ($BackendFile) { $taskArguments += [IO.Path]::GetFullPath($BackendFile) }
& 'D:\dotnet\dotnet.exe' @taskArguments 2>&1 | Tee-Object -FilePath (Join-Path $taskEvidence 'result.txt')
if ($LASTEXITCODE -ne 0) { throw "Close regression failed: $taskEvidence" }
Write-Output "Close regression evidence: $taskEvidence"
