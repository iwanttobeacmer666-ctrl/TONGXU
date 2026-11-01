param(
    [string]$SetupSource = (Join-Path $PSScriptRoot 'Setup.cs'),
    [string]$SignedMicrosoftFile = (Get-Command dotnet -ErrorAction Stop).Source
)
$ErrorActionPreference = 'Stop'
# No downloads, certificate-store changes, installation or elevation. Public test
# certificates are generated in memory; their private keys are never exported.
$taskWork = Join-Path $PSScriptRoot ('verification-signature-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $taskWork | Out-Null
$taskSubjects = @(
    'CN=.NET, O=Microsoft Corporation, C=US',
    'CN=Microsoft Corporation, O=Microsoft Corporation, C=US',
    'CN=.NET, O=Other Company, C=US',
    'CN=Microsoft Corporation, O=Other Company, C=US',
    'CN=.NET, C=US',
    'CN=.NET, O=Microsoft Corporation Evil, C=US',
    'CN=Other Product, O=Microsoft Corporation, C=US',
    'CN=.NET, O="Other, O=Microsoft Corporation", C=US',
    'CN=.NET, OU=Microsoft Corporation, O=Other Company, C=US'
)
for ($taskIndex = 0; $taskIndex -lt $taskSubjects.Count; $taskIndex++) {
    $taskKey = [Security.Cryptography.RSA]::Create(2048)
    try {
        $taskRequest = [Security.Cryptography.X509Certificates.CertificateRequest]::new(
            $taskSubjects[$taskIndex], $taskKey, [Security.Cryptography.HashAlgorithmName]::SHA256,
            [Security.Cryptography.RSASignaturePadding]::Pkcs1)
        $taskCert = $taskRequest.CreateSelfSigned([DateTimeOffset]::UtcNow.AddMinutes(-1), [DateTimeOffset]::UtcNow.AddDays(1))
        try { [IO.File]::WriteAllBytes((Join-Path $taskWork "$taskIndex.cer"), $taskCert.Export([Security.Cryptography.X509Certificates.X509ContentType]::Cert)) }
        finally { $taskCert.Dispose() }
    } finally { $taskKey.Dispose() }
}
$taskHarness = @'
using System;
using System.IO;
using System.Reflection;
using System.Security.Cryptography.X509Certificates;
internal static class RuntimeSignatureTests
{
    static int checks;
    static void Check(bool condition, string message) {
        if (!condition) throw new Exception(message);
        checks++; Console.WriteLine("PASS: " + message);
    }
    static bool Rejected(string path) {
        try { Signature.VerifyMicrosoft(path); return false; }
        catch (IOException) { return true; }
    }
    static int Main(string[] args) {
        try {
            // Real Windows trust + publisher verification, not a mock.
            Signature.VerifyMicrosoft(args[1]);
            Check(true, "trusted local Microsoft .NET binary accepted");
            var publisher = typeof(Signature).GetMethod("IsMicrosoftPublisher", BindingFlags.Static | BindingFlags.NonPublic);
            for (int i = 0; i < 9; i++) {
                using (var cert = new X509Certificate2(Path.Combine(args[0], i + ".cer")))
                    Check((bool)publisher.Invoke(null, new object[] { cert }) == (i < 2), "publisher certificate case " + i);
            }
            Check(Rejected(Assembly.GetExecutingAssembly().Location), "unsigned executable rejected");
            var altered = File.ReadAllBytes(args[1]);
            // Change the DOS header in the Authenticode-covered portion of the PE.
            altered[2] ^= 1;
            var tampered = Path.Combine(args[0], "tampered.exe"); File.WriteAllBytes(tampered, altered);
            Check(Rejected(tampered), "tampered signed executable rejected");
            Console.WriteLine("PASS: " + checks + " signature checks. No installation performed.");
            return 0;
        } catch (Exception e) { Console.Error.WriteLine(e); return 1; }
    }
}
'@
$taskHarnessPath = Join-Path $taskWork 'RuntimeSignatureTests.cs'
[IO.File]::WriteAllText($taskHarnessPath, $taskHarness, [Text.UTF8Encoding]::new($false))
$taskCompiler = Join-Path $env:WINDIR 'Microsoft.NET\Framework64\v4.0.30319\csc.exe'
$taskExe = Join-Path $taskWork 'RuntimeSignatureTests.exe'
& $taskCompiler /nologo /target:exe /platform:x64 /main:RuntimeSignatureTests "/out:$taskExe" /reference:System.dll /reference:System.Core.dll /reference:System.Net.Http.dll /reference:System.Drawing.dll /reference:System.Windows.Forms.dll /reference:System.IO.Compression.dll /reference:System.IO.Compression.FileSystem.dll /reference:System.Web.Extensions.dll $SetupSource (Join-Path $PSScriptRoot 'OnlineRelease.cs') $taskHarnessPath
if ($LASTEXITCODE -ne 0) { throw 'Signature test compilation failed.' }
& $taskExe $taskWork $SignedMicrosoftFile 2>&1 | Tee-Object -FilePath (Join-Path $taskWork 'result.txt')
if ($LASTEXITCODE -ne 0) { throw "Signature tests failed. Evidence: $taskWork" }
Write-Output "Signature evidence: $taskWork"
