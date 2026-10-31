using System.Net;
using System.Security.Cryptography;
using System.Text;
using System.Text.Json;
using Tongxu.Desktop;

var evidence = Path.GetFullPath(args.Single());
Directory.CreateDirectory(evidence);
int checks = 0;
void Assert(bool condition, string message) { if (!condition) throw new Exception(message); checks++; }
void Reject(Action action, string message) { try { action(); } catch { checks++; return; } throw new Exception(message); }
async Task RejectAsync(Func<Task> action, string message) { try { await action(); } catch { checks++; return; } throw new Exception(message); }
byte[] package = [0xd0, 0xcf, 0x11, 0xe0, 0xa1, 0xb1, 0x1a, 0xe1, 1, 2, 3];
var digest = Convert.ToHexString(SHA256.HashData(package)).ToLowerInvariant();
byte[] Release(string tag = "v2.10.0", bool draft = false, bool prerelease = false, string? url = null, string? sha = null, string? name = null) => JsonSerializer.SerializeToUtf8Bytes(new {
    tag_name = tag, draft, prerelease, body = "Release notes <script> never executes",
    assets = new[] { new { name = name ?? UpdateService.AssetName(tag, "x64"), size = package.Length, digest = "sha256:" + (sha ?? digest), browser_download_url = url ?? $"https://github.com/{UpdateService.Repository}/releases/download/{tag}/{UpdateService.AssetName(tag,"x64")}" } }
});
var current = new Version(2, 9, 0);
var release = UpdateService.ParseRelease(Release(), current, "x64")!;
Assert(release.Version == new Version(2,10,0), "numeric version comparison");
Assert(UpdateService.ParseRelease(Release("v2.8.0"), current, "x64") == null, "downgrade offered");
Assert(UpdateService.ParseRelease(Release("v2.9.0"), current, "x64") == null, "same version offered");
Assert(UpdateService.ParseRelease(Release(draft:true), current, "x64") == null, "draft offered");
Assert(UpdateService.ParseRelease(Release(prerelease:true), current, "x64") == null, "prerelease offered");
foreach (var tag in new[] { "v2.0.0-beta", "v02.1.0", "2.1", "2.1.0.0", "../../2.0.0", "99999999999.0.0", "v2.0.0\n" }) Reject(() => UpdateService.ParseVersion(tag), "invalid version accepted");
Reject(() => UpdateService.ParseRelease(Release(url:"https://evil.example/x.msi"), current,"x64"), "external URL accepted");
Reject(() => UpdateService.ParseRelease(Release(sha:"bad"), current,"x64"), "missing digest accepted");
Reject(() => UpdateService.ParseRelease(Release(name:"Tongxu-2.10.0-win-arm64.msi"), current,"x64"), "wrong architecture accepted");
var now = DateTimeOffset.UtcNow;
Assert(UpdateService.IsDue(new(),now), "first check skipped");
Assert(!UpdateService.IsDue(new(now.AddHours(-23)),now), "daily limit ignored");
Assert(UpdateService.IsDue(new(now.AddDays(-1)),now), "next day not checked");
using (var service = new UpdateService(Path.Combine(evidence,"state"),new FixtureHandler(_ => new(HttpStatusCode.NotFound)))) {
    await service.SaveStateAsync(new(now,"2.10.0"));
    Assert(service.LoadState().SkippedVersion == "2.10.0", "skip persistence");
    Assert(await service.CheckAsync(default) == null,"404 release handling");
}
using (var service = new UpdateService(Path.Combine(evidence,"success"),new FixtureHandler(_ => new(HttpStatusCode.OK) {Content = new ByteArrayContent(package)}))) {
    var path = await service.DownloadAsync(release,null,default);
    Assert(File.ReadAllBytes(path).SequenceEqual(package),"download changed");
    await service.SavePendingAsync(release,path);
    Assert((await service.LoadPendingAsync(default))?.Package == path,"pending update lost on restart");
    File.WriteAllBytes(path,[1,2,3]);
    await RejectAsync(() => service.LoadPendingAsync(default),"tampered pending package accepted");
}
foreach (var scenario in new[] {"bad-hash","truncated","oversized","redirect"}) {
    var runtime = Path.Combine(evidence,scenario);
    using var service = new UpdateService(runtime,new FixtureHandler(_ => scenario switch {
        "redirect" => new(HttpStatusCode.Found) { Headers = { Location = new Uri("https://evil.example/setup.msi") } },
        "truncated" => new(HttpStatusCode.OK) { Content = new ByteArrayContent(package[..^1]) },
        "oversized" => new(HttpStatusCode.OK) { Content = new ByteArrayContent(package.Concat(package).ToArray()) },
        _ => new(HttpStatusCode.OK) { Content = new ByteArrayContent(new byte[package.Length]) }
    }));
    await RejectAsync(() => service.DownloadAsync(release,null,default),scenario + " accepted");
    Assert(!Directory.EnumerateFiles(Path.Combine(runtime,"updates"),"*.msi").Any(),"partial package retained");
}
using (var service = new UpdateService(Path.Combine(evidence,"redirect-success"),new FixtureHandler(request => request.RequestUri!.Host == "github.com"
    ? new(HttpStatusCode.Found) {Headers = {Location = new Uri("https://release-assets.githubusercontent.com/test")}}
    : new(HttpStatusCode.OK) {Content = new ByteArrayContent(package)}))) {
    Assert(File.Exists(await service.DownloadAsync(release,null,default)),"GitHub CDN redirect rejected");
}
var layout = Path.Combine(evidence,"layout"); Directory.CreateDirectory(layout);
Assert(AppPaths.Runtime(layout) == Path.Combine(layout,"runtime"),"legacy paths changed");
File.WriteAllText(Path.Combine(layout,"installed-layout.json"),"{\"Version\":1}");
Assert(AppPaths.Runtime(layout) == Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData),"Tongxu","runtime"),"installed data not separated");
File.WriteAllText(Path.Combine(layout,"installed-layout.json"),"{\"Version\":2}");
Reject(() => AppPaths.Runtime(layout),"unknown installed layout accepted");
using (var service = new UpdateService(Path.Combine(evidence,"cancel"),new FixtureHandler(_ => new(HttpStatusCode.OK) {Content = new ByteArrayContent(package)}))) {
    using var cancel = new CancellationTokenSource(); cancel.Cancel();
    await RejectAsync(() => service.DownloadAsync(release,null,cancel.Token),"cancelled download continued");
    Assert(!Directory.EnumerateFiles(Path.Combine(evidence,"cancel","updates"),"*.msi").Any(),"cancel left partial installer");
}
using (var service = new UpdateService(Path.Combine(evidence,"api-overflow"),new FixtureHandler(_ => new(HttpStatusCode.OK) {Content = new StreamContent(new NonSeekableStream(new byte[1024*1024+1]))})))
    await RejectAsync(() => service.CheckAsync(default),"unbounded API response accepted");
using (var service = new UpdateService(Path.Combine(evidence,"chunked-overflow"),new FixtureHandler(_ => new(HttpStatusCode.OK) {Content = new StreamContent(new NonSeekableStream(package.Concat(package).ToArray()))})))
    await RejectAsync(() => service.DownloadAsync(release,null,default),"chunked oversized download accepted");
using (var service = new UpdateService(Path.Combine(evidence,"api-error"),new FixtureHandler(_ => new(HttpStatusCode.Forbidden))))
    await RejectAsync(() => service.CheckAsync(default),"API error reported as up to date");
File.WriteAllText(Path.Combine(evidence,"results.json"),JsonSerializer.Serialize(new { checks, networkRequests = 0, actualInstallations = 0 }));
Console.WriteLine($"PASS: {checks} offline update checks; no network or installation.");

sealed class FixtureHandler(Func<HttpRequestMessage,HttpResponseMessage> respond) : HttpMessageHandler {
    protected override Task<HttpResponseMessage> SendAsync(HttpRequestMessage request,CancellationToken token) { token.ThrowIfCancellationRequested(); return Task.FromResult(respond(request)); }
}
sealed class NonSeekableStream(byte[] bytes) : MemoryStream(bytes) { public override bool CanSeek => false; }
namespace Tongxu.Desktop { internal static class Localization { public static string T(string source) => source; } }
