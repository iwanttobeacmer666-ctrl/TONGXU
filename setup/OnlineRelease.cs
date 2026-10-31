using System;
using System.Collections.Generic;
using System.IO;
using System.IO.Compression;
using System.Linq;
using System.Net;
using System.Net.Http;
using System.Security.Cryptography;
using System.Text;
using System.Text.RegularExpressions;
using System.Threading;
using System.Threading.Tasks;

internal sealed class OnlineRelease
{
    internal const string Repository = "iwanttobeacmer666-ctrl/TONGXU";
    internal const long MaxPackage = 256L * 1024 * 1024;
    internal string Version, Hash;
    internal Uri Url;
    internal long Size;
    internal static string VersionOf(string tag)
    {
        if (tag == null || !Regex.IsMatch(tag, @"\Av?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\z")) throw new InvalidDataException("正式版版本号无效。");
        Version parsed; if (!System.Version.TryParse(tag.TrimStart('v'), out parsed)) throw new InvalidDataException("正式版版本号无效。");
        return tag.TrimStart('v');
    }
    internal static OnlineRelease Parse(string json)
    {
        var value = Package.Json.Deserialize<Dictionary<string, object>>(json);
        if ((bool)value["draft"] || (bool)value["prerelease"]) throw new InvalidDataException("最新发布不是正式版。");
        var tag = (string)value["tag_name"]; var version = VersionOf(tag);
        var name = "Tongxu-" + version + "-win-x64.zip";
        var matches = ((System.Collections.IEnumerable)value["assets"]).Cast<Dictionary<string, object>>().Where(a => (string)a["name"] == name).ToArray();
        if (matches.Length != 1) throw new InvalidDataException("最新正式版缺少匹配的程序包：" + name + "。请等待发布者补齐，不会退回旧版。");
        var asset = matches[0]; var size = Convert.ToInt64(asset["size"]);
        var digest = asset.ContainsKey("digest") ? asset["digest"] as string : null;
        if (size <= 0 || size > MaxPackage || digest == null || !Regex.IsMatch(digest, @"\Asha256:[a-fA-F0-9]{64}\z")) throw new InvalidDataException("程序包缺少有效大小或 SHA-256 校验信息。");
        var expected = "https://github.com/" + Repository + "/releases/download/" + Uri.EscapeDataString(tag) + "/" + name;
        if ((string)asset["browser_download_url"] != expected) throw new InvalidDataException("程序包地址不属于指定发布仓库。");
        return new OnlineRelease { Version = version, Hash = digest.Substring(7).ToLowerInvariant(), Size = size, Url = new Uri(expected) };
    }
    internal static async Task<OnlineRelease> LatestAsync(CancellationToken token)
    {
        var bytes = await FetchAsync(new Uri("https://api.github.com/repos/" + Repository + "/releases/latest"), 2 * 1024 * 1024, null, token, null);
        return Parse(Encoding.UTF8.GetString(bytes));
    }
    internal async Task<byte[]> DownloadAsync(CancellationToken token, Action<string, int> report)
    {
        var bytes = await FetchAsync(Url, Size, Size, token, p => report("正在下载最新正式版同序… " + p + "%", 65 + p / 5));
        VerifyDownload(bytes);
        return bytes;
    }
    internal void VerifyDownload(byte[] bytes)
    {
        if (bytes.LongLength != Size || Package.Digest(new MemoryStream(bytes), SHA256.Create()) != Hash) throw new InvalidDataException("程序包大小或 SHA-256 校验失败，未安装。");
    }
    internal static bool Allowed(Uri url)
    {
        return url.IsAbsoluteUri && url.Scheme == "https" && url.Port == 443 && url.UserInfo == "" && url.Fragment == "" &&
            new[] { "api.github.com", "github.com", "release-assets.githubusercontent.com", "objects.githubusercontent.com" }.Contains(url.Host);
    }
    static async Task<byte[]> FetchAsync(Uri url, long max, long? expected, CancellationToken token, Action<int> progress)
    {
        ServicePointManager.SecurityProtocol = SecurityProtocolType.Tls12;
        using (var timeout = CancellationTokenSource.CreateLinkedTokenSource(token))
        using (var client = new HttpClient(new HttpClientHandler { AllowAutoRedirect = false })) {
            timeout.CancelAfter(TimeSpan.FromMinutes(15));
            client.Timeout = System.Threading.Timeout.InfiniteTimeSpan;
            client.DefaultRequestHeaders.UserAgent.ParseAdd("TONGXU-Setup/1.0.0");
            for (var redirect = 0; redirect < 6; redirect++) {
                if (!Allowed(url)) throw new InvalidDataException("程序下载重定向到不受信任的地址。");
                using (var response = await client.GetAsync(url, HttpCompletionOption.ResponseHeadersRead, timeout.Token)) {
                    var code = (int)response.StatusCode;
                    if (new[] { 301, 302, 303, 307, 308 }.Contains(code)) {
                        if (response.Headers.Location == null) throw new IOException("下载重定向缺少地址。");
                        url = new Uri(url, response.Headers.Location); continue;
                    }
                    if (response.StatusCode == HttpStatusCode.NotFound) throw new IOException("尚无可下载的正式发布包。请联系发布者，或稍后重试。");
                    if ((int)response.StatusCode == 403 || (int)response.StatusCode == 429) throw new IOException("发布服务访问受限，请稍后重试。");
                    response.EnsureSuccessStatusCode();
                    if (response.Content.Headers.ContentLength > max) throw new InvalidDataException("下载内容超过大小限制。");
                    using (var input = await response.Content.ReadAsStreamAsync()) using (var output = new MemoryStream()) {
                        var buffer = new byte[65536]; int count;
                        while ((count = await input.ReadAsync(buffer, 0, buffer.Length, timeout.Token)) != 0) {
                            if (output.Length + count > max) throw new InvalidDataException("下载内容超过大小限制。");
                            output.Write(buffer, 0, count);
                            if (progress != null && expected.HasValue) progress((int)(output.Length * 100 / expected.Value));
                        }
                        if (expected.HasValue && output.Length != expected.Value) throw new InvalidDataException("程序包下载不完整。");
                        return output.ToArray();
                    }
                }
            }
            throw new IOException("程序下载重定向次数过多。");
        }
    }
    internal static Dictionary<string, string> PackageManifest(byte[] bytes, string version)
    {
        using (var archive = new ZipArchive(new MemoryStream(bytes), ZipArchiveMode.Read)) {
            var entries = archive.Entries.Where(e => e.FullName == "tongxu-package.json").ToArray();
            if (entries.Length != 1 || entries[0].Length > 1024 * 1024) throw new InvalidDataException("程序包缺少有效发布清单。");
            Dictionary<string, object> data;
            using (var reader = new StreamReader(entries[0].Open(), Encoding.UTF8)) data = Package.Json.Deserialize<Dictionary<string, object>>(reader.ReadToEnd());
            if ((string)data["Version"] != version || Convert.ToInt32(data["SetupProtocol"]) != 1 || Convert.ToInt32(data["RuntimeMajor"]) != 10) throw new InvalidDataException("最新程序包版本或安装协议不匹配，请使用新版安装器。");
            var files = ((Dictionary<string, object>)data["Files"]).ToDictionary(p => p.Key, p => (string)p.Value, StringComparer.OrdinalIgnoreCase);
            if (files.Count == 0 || files.Count > 10000 || files.ContainsKey("tongxu-package.json") || files.Any(p => !Regex.IsMatch(p.Value, @"\A[a-f0-9]{64}\z"))) throw new InvalidDataException("程序文件清单无效。");
            foreach (var required in new[] { "bin/desktop/tongxu.exe", "bin/desktop/tongxu.dll", "bin/desktop/tongxu.runtimeconfig.json", "bin/group-assistant.exe", "Desktop-Operator.ps1", "Operator-Setup.ps1", "Operator-Paths.ps1", "Runtime-Paths.ps1", "config.group-assistant.json", "installed-layout.json" })
                if (!files.ContainsKey(required)) throw new InvalidDataException("程序包缺少必要文件：" + required);
            return files;
        }
    }
}
