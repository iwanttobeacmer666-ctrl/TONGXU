using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.Drawing;
using System.IO;
using System.IO.Compression;
using System.Linq;
using System.Net;
using System.Reflection;
using System.Runtime.InteropServices;
using System.Security.Cryptography;
using System.Security.Cryptography.X509Certificates;
using System.Text;
using System.Threading;
using System.Threading.Tasks;
using System.Web.Script.Serialization;
using System.Windows.Forms;
using Microsoft.Win32;

[assembly: AssemblyTitle("TONGXU Setup")]
[assembly: AssemblyProduct("TONGXU")]
[assembly: AssemblyVersion("1.0.0.0")]
[assembly: AssemblyFileVersion("1.0.0.0")]
[assembly: AssemblyInformationalVersion("1.0.0")]

internal static class Package
{
    internal static readonly JavaScriptSerializer Json = new JavaScriptSerializer { MaxJsonLength = 2 * 1024 * 1024 };
    internal const string RegistryKey = @"Software\Microsoft\Windows\CurrentVersion\Uninstall\Tongxu";
    internal static string Exe { get { return Assembly.GetExecutingAssembly().Location; } }
    internal static string Local { get { return Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData); } }
    internal static string Digest(Stream stream, HashAlgorithm hash) { using (hash) return BitConverter.ToString(hash.ComputeHash(stream)).Replace("-", "").ToLowerInvariant(); }
    internal static string FileHash(string path) { using (var s = File.OpenRead(path)) return Digest(s, SHA256.Create()); }
    internal static byte[] Resource(string name)
    {
        using (var s = Assembly.GetExecutingAssembly().GetManifestResourceStream(name))
        using (var m = new MemoryStream()) { if (s == null) throw new InvalidDataException("缺少安装资源。"); s.CopyTo(m); return m.ToArray(); }
    }
    internal static string SafePath(string root, string relative)
    {
        if (String.IsNullOrWhiteSpace(relative) || relative.Contains(":") || relative.Contains("\\") || relative.Split('/').Any(p => p == ".." || p == "." || p.Length == 0) || Path.IsPathRooted(relative))
            throw new InvalidDataException("不安全的安装路径。");
        var full = Path.GetFullPath(Path.Combine(root, relative.Replace('/', Path.DirectorySeparatorChar)));
        if (!full.StartsWith(Path.GetFullPath(root).TrimEnd('\\') + "\\", StringComparison.OrdinalIgnoreCase)) throw new InvalidDataException("安装路径越界。");
        return full;
    }
    internal static void NoLinks(string path)
    {
        for (var d = new DirectoryInfo(Path.GetFullPath(path)); d != null; d = d.Parent)
            if (d.Exists && (d.Attributes & FileAttributes.ReparsePoint) != 0) throw new IOException("安装目录不能使用符号链接或目录联接。");
    }
    internal static void Extract(string root, byte[] zipBytes, Dictionary<string, string> manifest)
    {
        NoLinks(root);
        if (Directory.Exists(root) && Directory.EnumerateFileSystemEntries(root).Any()) throw new IOException("解包目标必须为空。");
        Directory.CreateDirectory(root);
        var seen = new HashSet<string>(StringComparer.OrdinalIgnoreCase);
        long expanded = 0;
        using (var zip = new ZipArchive(new MemoryStream(zipBytes), ZipArchiveMode.Read))
            foreach (var entry in zip.Entries)
            {
                if (entry.FullName == "tongxu-package.json") continue;
                var dest = SafePath(root, entry.FullName);
                expanded += entry.Length;
                if (!manifest.ContainsKey(entry.FullName) || !seen.Add(entry.FullName) || entry.Length > 256 * 1024 * 1024 || expanded > 512L * 1024 * 1024) throw new InvalidDataException("安装包文件清单不匹配或超出大小限制。");
                Directory.CreateDirectory(Path.GetDirectoryName(dest));
                using (var input = entry.Open()) using (var output = new FileStream(dest, FileMode.CreateNew)) input.CopyTo(output);
                if (!String.Equals(FileHash(dest), manifest[entry.FullName], StringComparison.Ordinal)) throw new InvalidDataException("安装文件校验失败。");
            }
        if (seen.Count != manifest.Count) throw new InvalidDataException("安装包不完整。");
    }
    internal static bool Compatible(string value) { Version v; return Version.TryParse(value, out v) && v.Major == 10 && v.Minor == 0 && v.Build >= 0 && !value.Contains("-"); }
    internal static bool RuntimePresent()
    {
        var roots = new List<string> { Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.ProgramFiles), "dotnet") };
        using (var baseKey = RegistryKeyOpen())
        using (var key = baseKey.OpenSubKey(@"SOFTWARE\dotnet\Setup\InstalledVersions\x64"))
        { if (key != null && key.GetValue("InstallLocation") is string) roots.Add((string)key.GetValue("InstallLocation")); }
        return roots.Where(Directory.Exists).Any(root => new[] { "Microsoft.WindowsDesktop.App", "Microsoft.NETCore.App" }.All(fx => {
            var folder = Path.Combine(root, "shared", fx);
            return Directory.Exists(folder) && Directory.GetDirectories(folder).Any(d => Compatible(Path.GetFileName(d)) && File.Exists(Path.Combine(d, fx == "Microsoft.WindowsDesktop.App" ? "PresentationFramework.dll" : "System.Private.CoreLib.dll")));
        }));
    }
    static RegistryKey RegistryKeyOpen() { return Microsoft.Win32.RegistryKey.OpenBaseKey(RegistryHive.LocalMachine, RegistryView.Registry64); }
    internal static void Shortcut(string path, string target)
    {
        var shell = Activator.CreateInstance(Type.GetTypeFromProgID("WScript.Shell"));
        object link = null;
        try {
            link = shell.GetType().InvokeMember("CreateShortcut", BindingFlags.InvokeMethod, null, shell, new object[] { path });
            link.GetType().InvokeMember("TargetPath", BindingFlags.SetProperty, null, link, new object[] { target });
            link.GetType().InvokeMember("WorkingDirectory", BindingFlags.SetProperty, null, link, new object[] { Path.GetDirectoryName(target) });
            link.GetType().InvokeMember("Save", BindingFlags.InvokeMethod, null, link, null);
        } finally { if (link != null) Marshal.FinalReleaseComObject(link); Marshal.FinalReleaseComObject(shell); }
    }
    internal static List<FileStream> LockApplication()
    {
        var locks = new List<FileStream>();
        var runtime = Path.Combine(Local, @"Tongxu\runtime");
        try {
            if (Directory.Exists(runtime)) foreach (var name in new[] { "desktop.lock", "launcher.lock", "operator.lock", "service.lock" }) {
                var path = Path.Combine(runtime, name);
                if (File.Exists(path)) locks.Add(new FileStream(path, FileMode.Open, FileAccess.ReadWrite, FileShare.None));
            }
            return locks;
        } catch { foreach (var item in locks) item.Dispose(); throw new IOException("请先从托盘退出同序，等待服务与任务停止后重试。"); }
    }
}

internal sealed class SetupWindow : Form
{
    readonly TextBox destination = new TextBox();
    readonly Label status = new Label();
    readonly ProgressBar progress = new ProgressBar();
    readonly Button install = new Button(), browse = new Button(), open = new Button(), cancel = new Button();
    readonly CheckBox desktop = new CheckBox();
    CancellationTokenSource cancellation;
    bool busy;
    string installedDesktop;
    internal SetupWindow()
    {
        Text = "TONGXU · 同序安装程序"; ClientSize = new Size(650, 425); MinimumSize = Size; MaximumSize = Size;
        StartPosition = FormStartPosition.CenterScreen; Font = new Font("Microsoft YaHei UI", 10); BackColor = Color.FromArgb(247, 249, 252);
        using (var icon = Assembly.GetExecutingAssembly().GetManifestResourceStream("tongxu.ico")) if (icon != null) Icon = new Icon(icon);
        Controls.Add(new Label { Text = "安装同序 TONGXU", Font = new Font(Font.FontFamily, 23, FontStyle.Bold), Location = new Point(30, 27), Size = new Size(580, 48) });
        Controls.Add(new Label { Text = "联网获取最新正式版 · 自动检测运行环境 · 保留个人配置", Location = new Point(32, 85), Size = new Size(590, 45) });
        Controls.Add(new Label { Text = "安装位置", Location = new Point(32, 145), AutoSize = true });
        destination.SetBounds(32, 175, 490, 30);
        destination.Text = Path.Combine(Package.Local, @"Programs\Tongxu");
        using (var key = Registry.CurrentUser.OpenSubKey(Package.RegistryKey)) if (key != null && key.GetValue("InstallLocation") is string) destination.Text = (string)key.GetValue("InstallLocation");
        browse.Text = "浏览…"; browse.SetBounds(535, 173, 82, 33);
        browse.Click += delegate { using (var picker = new FolderBrowserDialog { Description = "选择同序安装目录（请使用专用目录）", SelectedPath = destination.Text }) if (picker.ShowDialog(this) == DialogResult.OK) destination.Text = picker.SelectedPath; };
        desktop.Text = "创建桌面快捷方式"; desktop.Checked = true; desktop.SetBounds(32, 222, 260, 30);
        status.Text = "点击开始后联网获取同序；缺少 .NET 时会先征求同意。"; status.SetBounds(32, 265, 585, 50);
        progress.SetBounds(32, 320, 585, 12);
        install.Text = "开始安装"; install.SetBounds(385, 357, 112, 38); install.Click += async delegate { await InstallAsync(); };
        cancel.Text = "关闭"; cancel.SetBounds(510, 357, 107, 38); cancel.Click += delegate { if (busy) cancellation.Cancel(); else Close(); };
        open.Text = "打开同序"; open.SetBounds(32, 357, 120, 38); open.Visible = false;
        open.Click += delegate { try { Process.Start(new ProcessStartInfo(installedDesktop) { UseShellExecute = true }); } catch (Exception e) { MessageBox.Show(this, e.Message, "无法打开同序"); } };
        Controls.AddRange(new Control[] { destination, browse, desktop, status, progress, install, open, cancel });
        FormClosing += delegate(object sender, FormClosingEventArgs e) { if (busy) { e.Cancel = true; cancellation.Cancel(); } };
    }
    void Report(string text, int percent) { status.Text = text; progress.Value = Math.Max(0, Math.Min(100, percent)); }
    async Task InstallAsync()
    {
        busy = true; install.Enabled = browse.Enabled = destination.Enabled = desktop.Enabled = false; open.Visible = false; cancel.Text = "取消";
        cancellation = new CancellationTokenSource();
        string stage = null;
        var locks = new List<FileStream>();
        try {
            if (!Environment.Is64BitOperatingSystem || Environment.OSVersion.Version < new Version(10, 0, 19045)) throw new IOException("此安装包支持 Windows 10 22H2 / Windows 11 x64。");
            var target = Path.GetFullPath(destination.Text).TrimEnd('\\');
            if (target.Contains('"') || target.StartsWith(@"\\") || target.Length > 150 || Path.GetPathRoot(target).TrimEnd('\\') == target) throw new IOException("请选择长度不超过 150 字符的本地专用安装目录。");
            Package.NoLinks(target);
            var ownerMarker = Path.Combine(target, ".tongxu-setup-owner");
            bool owned = File.Exists(ownerMarker) && File.ReadAllText(ownerMarker) == "TONGXU-Setup-v1";
            if (Directory.Exists(target) && Directory.EnumerateFileSystemEntries(target).Any() && !owned) throw new IOException("该目录已有其他文件，请选择空目录或由本安装器创建的同序目录。");
            Report("正在查询同序最新正式版…", 2);
            var release = await OnlineRelease.LatestAsync(cancellation.Token);
            Report("已找到最新正式版，正在检测运行环境…", 5);
            if (!Package.RuntimePresent()) {
                if (MessageBox.Show(this, "缺少兼容的 .NET 10 Desktop Runtime（x64）。\n是否从 Microsoft 下载并安装？\n此组件可能请求管理员权限，不会卸载其他 .NET 版本。", "准备运行环境", MessageBoxButtons.OKCancel, MessageBoxIcon.Information) != DialogResult.OK) throw new OperationCanceledException();
                await RuntimeDownload.InstallAsync(this, Report, cancellation.Token);
                if (!Package.RuntimePresent()) throw new IOException("尚未检测到兼容环境；如 Windows 要求重启，请重启后重新运行安装器。");
            }
            cancellation.Token.ThrowIfCancellationRequested();
            var bytes = await release.DownloadAsync(cancellation.Token, Report);
            var manifest = OnlineRelease.PackageManifest(bytes, release.Version);
            cancellation.Token.ThrowIfCancellationRequested();
            locks = Package.LockApplication();
            Directory.CreateDirectory(target);
            File.WriteAllText(ownerMarker, "TONGXU-Setup-v1");
            stage = Path.Combine(target, ".stage-" + Guid.NewGuid().ToString("N"));
            Report("正在校验并解包最新同序…", 85);
            await Task.Run(() => Package.Extract(stage, bytes, manifest));
            var actual = AssemblyName.GetAssemblyName(Path.Combine(stage, @"bin\desktop\tongxu.dll")).Version;
            if (actual.Major + "." + actual.Minor + "." + actual.Build != release.Version) throw new InvalidDataException("程序内部版本与正式发布版本不一致。");
            cancellation.Token.ThrowIfCancellationRequested();
            cancel.Enabled = false;
            var payloadHash = release.Hash;
            var app = Path.Combine(target, "app-" + payloadHash.Substring(0, 16));
            if (Directory.Exists(app)) {
                foreach (var item in manifest) if (!File.Exists(Package.SafePath(app, item.Key)) || Package.FileHash(Package.SafePath(app, item.Key)) != item.Value) throw new IOException("已有程序文件被修改，请另选安装目录。");
            } else { Directory.Move(stage, app); stage = null; }
            // All newly installed files have been verified. User data is never copied or migrated.
            File.Copy(Package.Exe, Path.Combine(app, "TONGXU-Uninstall.exe"), true);
            File.WriteAllText(Path.Combine(app, "package-files.json"), Package.Json.Serialize(manifest), new UTF8Encoding(false));
            installedDesktop = Path.Combine(app, @"bin\desktop\tongxu.exe");
            Report("正在创建快捷方式和卸载入口…", 90);
            var menu = Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.Programs), "TONGXU"); Directory.CreateDirectory(menu);
            Package.Shortcut(Path.Combine(menu, "同序.lnk"), installedDesktop);
            if (desktop.Checked) Package.Shortcut(Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.DesktopDirectory), "同序.lnk"), installedDesktop);
            var state = new Dictionary<string, string> { { "Product", "TONGXU" }, { "Current", Path.GetFileName(app) } };
            File.WriteAllText(Path.Combine(target, "tongxu-install.json"), Package.Json.Serialize(state), new UTF8Encoding(false));
            using (var key = Registry.CurrentUser.CreateSubKey(Package.RegistryKey)) {
                key.SetValue("DisplayName", "TONGXU · 同序"); key.SetValue("DisplayVersion", release.Version); key.SetValue("Publisher", "TONGXU");
                key.SetValue("InstallLocation", target); key.SetValue("DisplayIcon", installedDesktop);
                key.SetValue("UninstallString", "\"" + Path.Combine(app, "TONGXU-Uninstall.exe") + "\" --uninstall");
                key.SetValue("NoModify", 1); key.SetValue("NoRepair", 1);
            }
            Report("安装完成。打开同序后，配置 Codex CLI、企业微信和工作区。", 100);
            open.Visible = true; install.Text = "已安装";
        } catch (OperationCanceledException) { Report("已取消。可重新开始；已安装的运行环境会保留。", 0); install.Text = "重新安装"; }
        catch (Exception e) { Report("安装未完成：" + e.Message, 0); install.Text = "重试"; }
        finally {
            foreach (var item in locks) item.Dispose();
            if (stage != null && Directory.Exists(stage)) { try { Directory.Delete(stage, true); } catch { } }
            busy = false; cancel.Enabled = true; cancel.Text = "关闭"; browse.Enabled = destination.Enabled = desktop.Enabled = true; install.Enabled = !open.Visible; cancellation.Dispose();
        }
    }
}

internal static class RuntimeDownload
{
    const string Metadata = "https://builds.dotnet.microsoft.com/dotnet/release-metadata/10.0/releases.json";
    static bool Allowed(Uri url) { return url.Scheme == "https" && (url.Host == "builds.dotnet.microsoft.com" || url.Host == "download.visualstudio.microsoft.com"); }
    internal static async Task InstallAsync(Form owner, Action<string, int> report, CancellationToken token)
    {
        ServicePointManager.SecurityProtocol = SecurityProtocolType.Tls12;
        var dir = Path.Combine(Path.GetTempPath(), "Tongxu-setup-" + Guid.NewGuid().ToString("N")); Directory.CreateDirectory(dir);
        try {
            using (var web = new WebClient()) using (token.Register(web.CancelAsync)) {
                report("正在读取 Microsoft 运行环境版本信息…", 10);
                var jsonText = await web.DownloadStringTaskAsync(new Uri(Metadata)); token.ThrowIfCancellationRequested();
                var data = Package.Json.DeserializeObject(jsonText) as Dictionary<string, object>;
                Dictionary<string, object> selected = null;
                foreach (Dictionary<string, object> release in (System.Collections.IEnumerable)data["releases"]) {
                    if (!release.ContainsKey("windowsdesktop")) continue;
                    var component = (Dictionary<string, object>)release["windowsdesktop"];
                    if (!Package.Compatible((string)component["version"])) continue;
                    selected = ((System.Collections.IEnumerable)component["files"]).Cast<Dictionary<string, object>>().FirstOrDefault(f => (string)f["rid"] == "win-x64" && ((string)f["name"]).EndsWith(".exe", StringComparison.OrdinalIgnoreCase));
                    if (selected != null) break;
                }
                if (selected == null) throw new IOException("Microsoft 元数据中没有兼容的正式桌面运行环境。");
                var url = new Uri((string)selected["url"]); if (!Allowed(url)) throw new IOException("运行环境下载地址不受支持。");
                var path = Path.Combine(dir, "windowsdesktop-runtime.exe");
                web.DownloadProgressChanged += delegate(object s, DownloadProgressChangedEventArgs e) { report("正在下载 .NET 桌面运行环境… " + e.ProgressPercentage + "%", 15 + e.ProgressPercentage / 3); };
                await web.DownloadFileTaskAsync(url, path); token.ThrowIfCancellationRequested();
                string hash; using (var stream = File.OpenRead(path)) hash = Package.Digest(stream, SHA512.Create());
                if (!String.Equals(hash, (string)selected["hash"], StringComparison.OrdinalIgnoreCase)) throw new IOException("运行环境 SHA-512 校验失败，未执行安装。");
                Signature.VerifyMicrosoft(path);
                token.ThrowIfCancellationRequested(); report("正在安装运行环境。请处理 Windows 的权限提示；组件安装结束前请勿关闭。", 55);
                using (var process = Process.Start(new ProcessStartInfo(path, "/install /passive /norestart") { UseShellExecute = true, Verb = "runas", WorkingDirectory = dir })) {
                    await Task.Run(() => process.WaitForExit());
                    if (process.ExitCode != 0 && process.ExitCode != 3010) throw new IOException("运行环境安装未完成，退出码：" + process.ExitCode);
                    if (process.ExitCode == 3010) report("运行环境安装完成，Windows 提示需要重启。", 65);
                }
            }
        } finally { try { Directory.Delete(dir, true); } catch { } }
    }
}

internal static class Signature
{
    [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)] struct FileInfo { public uint Size; [MarshalAs(UnmanagedType.LPWStr)] public string Path; public IntPtr File, Subject; }
    [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)] struct TrustData { public uint Size; public IntPtr Policy, Sip; public uint UI, Revocation, Choice; public IntPtr File; public uint State; public IntPtr StateData, Url; public uint Flags, Context; }
    [DllImport("wintrust.dll", ExactSpelling = true)] static extern int WinVerifyTrust(IntPtr hwnd, ref Guid action, ref TrustData data);
    internal static void VerifyMicrosoft(string path)
    {
        var file = new FileInfo { Size = (uint)Marshal.SizeOf(typeof(FileInfo)), Path = path };
        var ptr = Marshal.AllocHGlobal(Marshal.SizeOf(file)); Marshal.StructureToPtr(file, ptr, false);
        try {
            var data = new TrustData { Size = (uint)Marshal.SizeOf(typeof(TrustData)), UI = 2, Choice = 1, File = ptr, Revocation = 1, Flags = 0x80 };
            var action = new Guid("00AAC56B-CD44-11d0-8CC2-00C04FC295EE");
            if (WinVerifyTrust(IntPtr.Zero, ref action, ref data) != 0) throw new IOException("Microsoft 安装程序签名或证书校验失败。");
            using (var cert = new X509Certificate2(X509Certificate.CreateFromSignedFile(path)))
                if (cert.GetNameInfo(X509NameType.SimpleName, false) != "Microsoft Corporation") throw new IOException("运行环境发布者不是 Microsoft Corporation。");
        } finally { Marshal.DestroyStructure(ptr, typeof(FileInfo)); Marshal.FreeHGlobal(ptr); }
    }
}

internal static class Program
{
    [STAThread] static int Main(string[] args)
    {
        if (args.Length == 3 && args[0] == "--self-test") return SelfTest(Path.GetFullPath(args[1]), Path.GetFullPath(args[2]));
        if (args.Length == 2 && args[0] == "--verify-latest") return VerifyLatest(Path.GetFullPath(args[1]));
        Application.EnableVisualStyles(); Application.SetCompatibleTextRenderingDefault(false);
        if (args.Length == 1 && args[0] == "--uninstall") {
            // Run outside the application directory so Windows can remove installed executables.
            var folder = Path.Combine(Path.GetTempPath(), "Tongxu-uninstall-" + Guid.NewGuid().ToString("N")); Directory.CreateDirectory(folder);
            var helper = Path.Combine(folder, "TONGXU-Uninstall.exe"); File.Copy(Package.Exe, helper);
            Process.Start(new ProcessStartInfo(helper, "--uninstall-from \"" + Path.GetDirectoryName(Package.Exe) + "\"") { UseShellExecute = true });
            return 0;
        }
        if (args.Length == 2 && args[0] == "--uninstall-from") { Uninstall(Path.GetFullPath(args[1])); return 0; }
        if (args.Length != 0) return 2;
        using (var mutex = new Mutex(false, @"Local\TONGXU-Setup-CurrentUser")) {
            if (!mutex.WaitOne(0)) { MessageBox.Show("同序安装器已经打开。"); return 1; }
            try { Application.Run(new SetupWindow()); } finally { mutex.ReleaseMutex(); }
        }
        return 0;
    }
    static int SelfTest(string root, string bundle)
    {
        try {
            if (Directory.Exists(root)) throw new IOException("Self-test target already exists.");
            Directory.CreateDirectory(root);
            var bytes = File.ReadAllBytes(bundle);
            var manifest = OnlineRelease.PackageManifest(bytes, "1.0.0");
            Package.Extract(Path.Combine(root, "payload"), bytes, manifest);
            foreach (var bad in new[] { "../escape", "/absolute", "C:/escape", "a/../../escape", "a\\escape", "a:stream", "./bad" }) {
                bool rejected = false; try { Package.SafePath(root, bad); } catch (InvalidDataException) { rejected = true; }
                if (!rejected) throw new Exception("Traversal accepted: " + bad);
            }
            if (!Package.Compatible("10.0.0") || !Package.Compatible("10.0.12") || Package.Compatible("10.0.0-preview.1") || Package.Compatible("9.0.8") || Package.Compatible("11.0.0")) throw new Exception("Runtime matching failed.");
            using (var memory = new MemoryStream()) {
                using (var archive = new ZipArchive(memory, ZipArchiveMode.Create, true)) using (var writer = new StreamWriter(archive.CreateEntry("../escape").Open())) writer.Write("bad");
                bool rejected = false; try { Package.Extract(Path.Combine(root, "malicious"), memory.ToArray(), manifest); } catch (InvalidDataException) { rejected = true; }
                if (!rejected) throw new Exception("Malicious archive accepted.");
            }
            using (var memory = new MemoryStream()) {
                var first = manifest.First();
                using (var archive = new ZipArchive(memory, ZipArchiveMode.Create, true)) using (var writer = new StreamWriter(archive.CreateEntry(first.Key).Open())) writer.Write("corrupted payload");
                bool rejected = false; try { Package.Extract(Path.Combine(root, "corrupt"), memory.ToArray(), manifest); } catch (InvalidDataException) { rejected = true; }
                if (!rejected) throw new Exception("Corrupt payload accepted.");
            }
            Application.EnableVisualStyles();
            using (var window = new SetupWindow()) using (var bitmap = new Bitmap(window.ClientSize.Width, window.ClientSize.Height)) {
                var handle = window.Handle;
                if (window.Controls.Count < 10) throw new Exception("Setup controls missing.");
                using (var graphics = Graphics.FromImage(bitmap)) {
                    graphics.Clear(window.BackColor);
                    foreach (Control control in window.Controls) {
                        if (control is Button && control.Text == "打开同序") continue;
                        var childHandle = control.Handle;
                        using (var child = new Bitmap(control.Width, control.Height)) { control.DrawToBitmap(child, new Rectangle(Point.Empty, child.Size)); graphics.DrawImageUnscaled(child, control.Location); }
                    }
                }
                bitmap.Save(Path.Combine(root, "setup-preview.png"));
            }
            VerifyReleaseSelection(bytes);
            if (Assembly.GetExecutingAssembly().GetManifestResourceNames().Any(n => n == "payload.zip" || n == "files.json")) throw new Exception("Online bootstrapper still embeds application payload.");
            File.WriteAllText(Path.Combine(root, "result.txt"), "PASS: external package manifest/hashes, extraction, path traversal/malicious/corrupt rejection, runtime matching, latest stable release selection/rejection, allowed redirect hosts, form render, no embedded application. No downloads or installation performed.");
            return 0;
        } catch (Exception e) { Directory.CreateDirectory(root); File.WriteAllText(Path.Combine(root, "failure.txt"), e.ToString()); return 1; }
    }
    static int VerifyLatest(string root)
    {
        if (Directory.Exists(root)) return 2;
        Directory.CreateDirectory(root);
        try {
            var release = OnlineRelease.LatestAsync(CancellationToken.None).GetAwaiter().GetResult();
            var bytes = release.DownloadAsync(CancellationToken.None, delegate(string message, int percent) { }).GetAwaiter().GetResult();
            var manifest = OnlineRelease.PackageManifest(bytes, release.Version);
            Package.Extract(Path.Combine(root, "payload"), bytes, manifest);
            var actual = AssemblyName.GetAssemblyName(Path.Combine(root, @"payload\bin\desktop\tongxu.dll")).Version;
            if (actual.Major + "." + actual.Minor + "." + actual.Build != release.Version) throw new InvalidDataException("Published assembly version mismatch.");
            File.WriteAllText(Path.Combine(root, "result.json"), Package.Json.Serialize(new { Version = release.Version, Sha256 = release.Hash, Size = bytes.LongLength, Files = manifest.Count, Installed = false, Success = true }));
            return 0;
        } catch (Exception e) { File.WriteAllText(Path.Combine(root, "failure.txt"), e.ToString()); return 1; }
    }
    static void VerifyReleaseSelection(byte[] bytes)
    {
        var asset = new Dictionary<string, object> {
            { "name", "Tongxu-1.0.0-win-x64.zip" }, { "size", bytes.LongLength },
            { "digest", "sha256:" + Package.Digest(new MemoryStream(bytes), SHA256.Create()) },
            { "browser_download_url", "https://github.com/" + OnlineRelease.Repository + "/releases/download/v1.0.0/Tongxu-1.0.0-win-x64.zip" }
        };
        var release = new Dictionary<string, object> { { "draft", false }, { "prerelease", false }, { "tag_name", "v1.0.0" }, { "assets", new object[] { asset } } };
        var selected = OnlineRelease.Parse(Package.Json.Serialize(release));
        if (selected.Version != "1.0.0") throw new Exception("Valid latest stable release rejected.");
        selected.VerifyDownload(bytes);
        ExpectReject(delegate { selected.VerifyDownload(new byte[0]); });
        var broken = (byte[])bytes.Clone(); broken[0] ^= 1;
        ExpectReject(delegate { selected.VerifyDownload(broken); });
        foreach (var field in new[] { "draft", "prerelease" }) {
            release[field] = true; ExpectReject(delegate { OnlineRelease.Parse(Package.Json.Serialize(release)); }); release[field] = false;
        }
        release["tag_name"] = "v1.0.0-preview"; ExpectReject(delegate { OnlineRelease.Parse(Package.Json.Serialize(release)); }); release["tag_name"] = "v1.0.0";
        var digest = asset["digest"]; asset["digest"] = ""; ExpectReject(delegate { OnlineRelease.Parse(Package.Json.Serialize(release)); }); asset["digest"] = digest;
        var url = asset["browser_download_url"]; asset["browser_download_url"] = "https://example.com/file.zip"; ExpectReject(delegate { OnlineRelease.Parse(Package.Json.Serialize(release)); }); asset["browser_download_url"] = url;
        release["assets"] = new object[0]; ExpectReject(delegate { OnlineRelease.Parse(Package.Json.Serialize(release)); });
        foreach (var bad in new[] { "http://github.com/a", "https://evil.example/a", "https://github.com:444/a", "https://user@github.com/a" }) if (OnlineRelease.Allowed(new Uri(bad))) throw new Exception("Untrusted redirect allowed.");
        foreach (var good in new[] { "https://github.com/a", "https://release-assets.githubusercontent.com/a" }) if (!OnlineRelease.Allowed(new Uri(good))) throw new Exception("Trusted redirect rejected.");
    }
    static void ExpectReject(Action action) { bool rejected = false; try { action(); } catch (InvalidDataException) { rejected = true; } if (!rejected) throw new Exception("Unsafe release metadata accepted."); }
    static void Uninstall(string app)
    {
        try {
            var target = Path.GetDirectoryName(app);
            Package.NoLinks(target);
            if (!Path.GetFileName(app).StartsWith("app-", StringComparison.Ordinal) || Package.FileHash(Package.Exe) != Package.FileHash(Path.Combine(app, "TONGXU-Uninstall.exe"))) throw new IOException("卸载器身份校验失败。");
            using (var key = Registry.CurrentUser.OpenSubKey(Package.RegistryKey)) {
                if (key == null || !String.Equals((string)key.GetValue("InstallLocation"), target, StringComparison.OrdinalIgnoreCase)) throw new IOException("此卸载入口不属于当前安装。");
            }
            if (MessageBox.Show("卸载同序程序？\n用户配置、登录信息和工作区文件会保留。", "卸载 TONGXU", MessageBoxButtons.OKCancel, MessageBoxIcon.Question) != DialogResult.OK) return;
            var locks = Package.LockApplication();
            try {
                foreach (var version in Directory.GetDirectories(target, "app-*")) {
                    Package.NoLinks(version);
                    var list = Path.Combine(version, "package-files.json"); if (!File.Exists(list)) continue;
                    var files = Package.Json.Deserialize<Dictionary<string, string>>(File.ReadAllText(list));
                    foreach (var file in files) {
                        var path = Package.SafePath(version, file.Key); Package.NoLinks(Path.GetDirectoryName(path));
                        if (File.Exists(path) && (File.GetAttributes(path) & FileAttributes.ReparsePoint) == 0 && Package.FileHash(path) == file.Value) File.Delete(path);
                    }
                    var oldUninstaller = Path.Combine(version, "TONGXU-Uninstall.exe");
                    // Only delete an older helper when its own version's manifest is intact and it is an ordinary executable.
                    if (File.Exists(oldUninstaller) && (File.GetAttributes(oldUninstaller) & FileAttributes.ReparsePoint) == 0) File.Delete(oldUninstaller);
                    File.Delete(list);
                    RemoveEmptyDirectories(version);
                }
                var shortcut = Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.DesktopDirectory), "同序.lnk");
                RemoveShortcut(shortcut, target);
                RemoveShortcut(Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.Programs), @"TONGXU\同序.lnk"), target);
                Registry.CurrentUser.DeleteSubKeyTree(Package.RegistryKey, false);
                var state = Path.Combine(target, "tongxu-install.json");
                if (File.Exists(state)) File.Delete(state);
                var marker = Path.Combine(target, ".tongxu-setup-owner");
                if (File.Exists(marker) && File.ReadAllText(marker) == "TONGXU-Setup-v1") File.Delete(marker);
                if (!Directory.EnumerateFileSystemEntries(target).Any()) Directory.Delete(target);
                MessageBox.Show("同序已卸载。用户配置、登录信息、工作区和修改过的文件已保留。");
            } finally { foreach (var item in locks) item.Dispose(); }
        } catch (Exception e) { MessageBox.Show(e.Message, "卸载未完成"); }
    }
    static void RemoveShortcut(string path, string root)
    {
        if (!File.Exists(path)) return;
        var shell = Activator.CreateInstance(Type.GetTypeFromProgID("WScript.Shell")); object link = null;
        try {
            link = shell.GetType().InvokeMember("CreateShortcut", BindingFlags.InvokeMethod, null, shell, new object[] { path });
            var target = (string)link.GetType().InvokeMember("TargetPath", BindingFlags.GetProperty, null, link, null);
            if (target.StartsWith(root + "\\", StringComparison.OrdinalIgnoreCase)) File.Delete(path);
        } finally { if (link != null) Marshal.FinalReleaseComObject(link); Marshal.FinalReleaseComObject(shell); }
    }
    static void RemoveEmptyDirectories(string root)
    {
        Package.NoLinks(root);
        foreach (var dir in Directory.GetDirectories(root)) {
            if ((File.GetAttributes(dir) & FileAttributes.ReparsePoint) != 0) continue;
            RemoveEmptyDirectories(dir);
        }
        if (!Directory.EnumerateFileSystemEntries(root).Any()) Directory.Delete(root);
    }
}
