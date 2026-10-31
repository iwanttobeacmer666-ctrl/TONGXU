using System.IO;
using System.Net;
using System.Net.Http;
using System.Reflection;
using System.Runtime.InteropServices;
using System.Security.Cryptography;
using System.Text.Json;
using System.Text.RegularExpressions;

namespace Tongxu.Desktop;

internal sealed record UpdateRelease(Version Version, string Tag, string Notes, Uri Download, long Size, string Sha256);
internal sealed record UpdateState(DateTimeOffset? LastAttempt = null, string SkippedVersion = "");
internal sealed record PendingUpdate(UpdateRelease Release, string Package);

internal sealed class UpdateService : IDisposable
{
    public const string Repository = "iwanttobeacmer666-ctrl/TONGXU";
    public static Version CurrentVersion => Assembly.GetExecutingAssembly().GetName().Version is { } v ? new(v.Major, v.Minor, v.Build) : new(1, 0, 0);
    public static string Architecture => RuntimeInformation.ProcessArchitecture switch
    {
        System.Runtime.InteropServices.Architecture.X64 => "x64",
        System.Runtime.InteropServices.Architecture.Arm64 => "arm64",
        _ => throw new PlatformNotSupportedException("Unsupported update architecture.")
    };
    public static string AssetName(string tag, string architecture) => $"Tongxu-{tag.TrimStart('v')}-win-{architecture}.msi";
    const long MaxInstaller = 2L * 1024 * 1024 * 1024;
    readonly HttpClient _http;
    readonly string _directory;
    public UpdateService(string runtime, HttpMessageHandler? handler = null)
    {
        _directory = Path.Combine(runtime, "updates");
        _http = new HttpClient(handler ?? new HttpClientHandler { AllowAutoRedirect = false });
        _http.Timeout = Timeout.InfiniteTimeSpan;
        _http.DefaultRequestHeaders.UserAgent.ParseAdd("Tongxu-Update/" + CurrentVersion);
        _http.DefaultRequestHeaders.Accept.ParseAdd("application/vnd.github+json");
    }
    public static Version ParseVersion(string tag)
    {
        if (!Regex.IsMatch(tag, @"\Av?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\z", RegexOptions.CultureInvariant) || !Version.TryParse(tag.TrimStart('v'), out var version))
            throw new InvalidDataException("Release version must be major.minor.patch.");
        return version;
    }
    public UpdateState LoadState()
    {
        var path = Path.Combine(_directory, "state.json");
        if (!File.Exists(path)) return new();
        if (new FileInfo(path).Length > 4096) throw new InvalidDataException("Update settings are too large.");
        return JsonSerializer.Deserialize<UpdateState>(File.ReadAllText(path)) ?? new();
    }
    public async Task SaveStateAsync(UpdateState state)
    {
        Directory.CreateDirectory(_directory);
        var path = Path.Combine(_directory, "state.json");
        var temp = path + "." + Guid.NewGuid().ToString("N") + ".tmp";
        try { await File.WriteAllTextAsync(temp, JsonSerializer.Serialize(state)); File.Move(temp, path, true); }
        finally { if (File.Exists(temp)) File.Delete(temp); }
    }
    public static bool IsDue(UpdateState state, DateTimeOffset now) => state.LastAttempt is not { } last || now - last >= TimeSpan.FromDays(1);
    public async Task SavePendingAsync(UpdateRelease release, string package)
    {
        var path = Path.Combine(_directory, "pending.json");
        var temp = path + "." + Guid.NewGuid().ToString("N") + ".tmp";
        try { await File.WriteAllTextAsync(temp, JsonSerializer.Serialize(new PendingUpdate(release, package))); File.Move(temp, path, true); }
        finally { if (File.Exists(temp)) File.Delete(temp); }
    }
    public async Task<(UpdateRelease Release, string Package)?> LoadPendingAsync(CancellationToken token)
    {
        var path = Path.Combine(_directory, "pending.json");
        if (!File.Exists(path)) return null;
        if (new FileInfo(path).Length > 128 * 1024) throw new InvalidDataException("Pending update too large.");
        var pending = JsonSerializer.Deserialize<PendingUpdate>(await File.ReadAllTextAsync(path, token)) ?? throw new InvalidDataException("Invalid pending update.");
        if (pending.Release.Version <= CurrentVersion) return null;
        var package = Path.GetFullPath(pending.Package);
        if (!string.Equals(Path.GetDirectoryName(package), Path.GetFullPath(_directory), StringComparison.OrdinalIgnoreCase) || !Regex.IsMatch(Path.GetFileName(package), "^package-[a-f0-9]{32}\\.msi$"))
            throw new InvalidDataException("Invalid pending package path.");
        await VerifyPackageAsync(package, pending.Release, token);
        return (pending.Release, package);
    }
    public async Task<UpdateRelease?> CheckAsync(CancellationToken token)
    {
        using var timeout = CancellationTokenSource.CreateLinkedTokenSource(token);
        timeout.CancelAfter(TimeSpan.FromSeconds(20));
        using var response = await _http.GetAsync($"https://api.github.com/repos/{Repository}/releases/latest", HttpCompletionOption.ResponseHeadersRead, timeout.Token);
        if (response.StatusCode == HttpStatusCode.NotFound) return null;
        response.EnsureSuccessStatusCode();
        await using var stream = await response.Content.ReadAsStreamAsync(timeout.Token);
        using var buffer = new MemoryStream();
        await CopyBoundedAsync(stream, buffer, 1024 * 1024, timeout.Token);
        return ParseRelease(buffer.ToArray(), CurrentVersion, Architecture);
    }
    internal static UpdateRelease? ParseRelease(byte[] bytes, Version current, string architecture)
    {
        using var json = JsonDocument.Parse(bytes);
        var root = json.RootElement;
        if (root.GetProperty("draft").GetBoolean() || root.GetProperty("prerelease").GetBoolean()) return null;
        var tag = root.GetProperty("tag_name").GetString() ?? "";
        var version = ParseVersion(tag);
        if (version <= current) return null;
        var name = AssetName(tag, architecture);
        var assets = root.GetProperty("assets").EnumerateArray().Where(a => a.GetProperty("name").GetString() == name).ToArray();
        if (assets.Length != 1) throw new InvalidDataException(Localization.T("新版本尚未提供匹配的安装包，请稍后重试。"));
        var asset = assets[0];
        var size = asset.GetProperty("size").GetInt64();
        var digest = asset.GetProperty("digest").GetString() ?? "";
        if (size <= 0 || size > MaxInstaller || !Regex.IsMatch(digest, "^sha256:[a-fA-F0-9]{64}$"))
            throw new InvalidDataException(Localization.T("安装包缺少有效校验信息，已停止更新。"));
        var url = new Uri(asset.GetProperty("browser_download_url").GetString() ?? "");
        var expected = $"https://github.com/{Repository}/releases/download/{Uri.EscapeDataString(tag)}/{name}";
        if (!string.Equals(url.AbsoluteUri, expected, StringComparison.Ordinal)) throw new InvalidDataException("Unexpected release asset URL.");
        var notes = root.TryGetProperty("body", out var body) && body.ValueKind == JsonValueKind.String ? body.GetString() ?? "" : "";
        return new(version, tag, notes.Length > 24000 ? notes[..24000] : notes, url, size, digest[7..].ToLowerInvariant());
    }
    public async Task<string> DownloadAsync(UpdateRelease release, IProgress<double>? progress, CancellationToken token)
    {
        Directory.CreateDirectory(_directory);
        var path = Path.Combine(_directory, "package-" + Guid.NewGuid().ToString("N") + ".msi");
        using var timeout = CancellationTokenSource.CreateLinkedTokenSource(token);
        timeout.CancelAfter(TimeSpan.FromMinutes(30));
        try
        {
            using var response = await GetAssetAsync(release.Download, timeout.Token);
            response.EnsureSuccessStatusCode();
            if (response.Content.Headers.ContentLength is { } size && size != release.Size) throw new InvalidDataException("Installer size mismatch.");
            await using (var input = await response.Content.ReadAsStreamAsync(timeout.Token))
            await using (var output = new FileStream(path, FileMode.CreateNew, FileAccess.Write, FileShare.None, 65536, true))
                await CopyBoundedAsync(input, output, release.Size, timeout.Token, progress);
            await VerifyPackageAsync(path, release, timeout.Token);
            return path;
        }
        catch { if (File.Exists(path)) File.Delete(path); throw; }
    }
    async Task<HttpResponseMessage> GetAssetAsync(Uri uri, CancellationToken token)
    {
        for (int i = 0; i < 6; i++)
        {
            if (uri.Scheme != "https" || uri.Port != 443 || uri.UserInfo != "" || uri.Fragment != "" ||
                uri.Host is not ("github.com" or "release-assets.githubusercontent.com" or "objects.githubusercontent.com"))
                throw new InvalidDataException("Untrusted download redirect.");
            var response = await _http.GetAsync(uri, HttpCompletionOption.ResponseHeadersRead, token);
            if ((int)response.StatusCode is not (301 or 302 or 303 or 307 or 308)) return response;
            var location = response.Headers.Location;
            response.Dispose();
            if (location == null) throw new InvalidDataException("Missing download redirect.");
            uri = new Uri(uri, location);
        }
        throw new InvalidDataException("Too many download redirects.");
    }
    internal static async Task VerifyPackageAsync(string path, UpdateRelease release, CancellationToken token)
    {
        if (release.Size <= 0 || release.Size > MaxInstaller || !Regex.IsMatch(release.Sha256, @"\A[a-fA-F0-9]{64}\z")) throw new InvalidDataException("Invalid installer metadata.");
        await using var input = new FileStream(path, FileMode.Open, FileAccess.Read, FileShare.Read, 65536, true);
        if (input.Length != release.Size) throw new InvalidDataException("Installer size mismatch.");
        var digest = Convert.ToHexString(await SHA256.HashDataAsync(input, token));
        if (!digest.Equals(release.Sha256, StringComparison.OrdinalIgnoreCase)) throw new InvalidDataException(Localization.T("安装包校验失败，请重新下载。"));
        input.Position = 0;
        byte[] header = new byte[8];
        if (await input.ReadAsync(header, token) != 8 || !header.SequenceEqual(new byte[] { 0xd0, 0xcf, 0x11, 0xe0, 0xa1, 0xb1, 0x1a, 0xe1 }))
            throw new InvalidDataException("Not a Windows Installer package.");
    }
    static async Task CopyBoundedAsync(Stream input, Stream output, long max, CancellationToken token, IProgress<double>? progress = null)
    {
        var buffer = new byte[65536]; long count = 0; int read;
        while ((read = await input.ReadAsync(buffer, token)) != 0)
        {
            count += read;
            if (count > max) throw new InvalidDataException("Download exceeds expected size.");
            await output.WriteAsync(buffer.AsMemory(0, read), token);
            progress?.Report((double)count / max);
        }
    }
    public void Dispose() => _http.Dispose();
}
