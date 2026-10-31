using System.Diagnostics;
using System.IO;
using System.Text.Json;
using System.Text.Json.Nodes;
using System.Windows;
using System.Windows.Controls;

namespace Tongxu.Desktop;

public partial class MainWindow
{
    UpdateService? _updates;
    UpdateRelease? _release;
    string? _updatePackage;
    bool _updateBusy, _updateInstalling;
    readonly CancellationTokenSource _updateLifetime = new();
    TextBlock _updateStatus = null!;
    TextBlock _updateVersionText = null!;
    TextBox _releaseNotes = null!;
    Button _checkUpdate = null!, _downloadUpdate = null!, _installUpdate = null!, _skipUpdate = null!, _cancelUpdate = null!;
    CancellationTokenSource? _updateOperation;

    void AddUpdateSettings(StackPanel stack)
    {
        stack.Children.Add(T("软件更新", 17, true));
        _updateVersionText = T(Localization.F($"当前版本：{UpdateService.CurrentVersion}"), 13);
        stack.Children.Add(_updateVersionText);
        stack.Children.Add(T("启动后每天最多自动检查一次，仅接收正式稳定版。", 12, false, true));
        _updateStatus = T("尚未检查更新", 13);
        stack.Children.Add(_updateStatus);
        _releaseNotes = new TextBox { IsReadOnly = true, TextWrapping = TextWrapping.Wrap, MaxHeight = 170, VerticalScrollBarVisibility = ScrollBarVisibility.Auto, Visibility = Visibility.Collapsed };
        stack.Children.Add(_releaseNotes);
        var row = Flow();
        Button Action(string label, Func<Task> action)
        {
            var button = new Button(); Localization.Bind(button, ContentControl.ContentProperty, label);
            button.Click += async (_, _) => { try { await action(); } catch (Exception ex) { _updateStatus.Text = SafeText.Clean(ex.Message); } };
            row.Children.Add(button); return button;
        }
        _checkUpdate = Action("检查更新", () => CheckUpdatesAsync(true));
        _downloadUpdate = Action("下载更新", DownloadUpdateAsync);
        _installUpdate = Action("重启并安装更新", () => InstallUpdateAsync());
        _skipUpdate = Action("跳过此版本", SkipUpdateAsync);
        _cancelUpdate = Action("取消更新操作", async () =>
        {
            if (_updateBusy) { _updateOperation?.Cancel(); return; }
            if (!_status.B("UpdatePaused")) return;
            var roster = await Client.SendAsync(new() { ["Action"] = "list" }); Check(roster);
            Check(await Client.SendAsync(new() { ["Action"] = "update-resume", ["Version"] = roster.S("Version") }));
            await RefreshAsync(true);
        });
        Action("打开更新日志", () =>
        {
            var directory = Path.Combine(Client.Runtime, "updates");
            Directory.CreateDirectory(directory);
            Process.Start(new ProcessStartInfo(directory) { UseShellExecute = true });
            return Task.CompletedTask;
        });
        stack.Children.Add(row);
        UpdateButtons();
    }
    void UpdateButtons()
    {
        _checkUpdate.IsEnabled = !_updateBusy;
        _downloadUpdate.IsEnabled = !_updateBusy && _release != null && _updatePackage == null;
        _installUpdate.IsEnabled = !_updateBusy && _updatePackage != null;
        _skipUpdate.IsEnabled = !_updateBusy && _release != null;
        _cancelUpdate.IsEnabled = _updateBusy && !_updateInstalling || !_updateBusy && _status.B("UpdatePaused");
    }
    async Task StartUpdateChecksAsync()
    {
        if (_closing) return;
        _updates = new UpdateService(Client.Runtime);
        try
        {
            var pending = await _updates.LoadPendingAsync(_updateLifetime.Token);
            if (pending != null)
            {
                (_release, _updatePackage) = pending.Value;
                ShowRelease();
                _updateStatus.Text = Localization.T("更新已下载，可稍后重启安装。");
            }
        }
        catch (Exception) { /* A damaged cache never prevents normal startup. */ }
        await CheckUpdatesAsync(false);
        var resultPath = Path.Combine(Client.Runtime, "updates", "install-result.json");
        try
        {
            if (File.Exists(resultPath) && new FileInfo(resultPath).Length <= 16384)
            {
                var result = JsonNode.Parse(await File.ReadAllTextAsync(resultPath, _updateLifetime.Token));
                if (result != null && !result.B("Success") && UpdateService.ParseVersion(result.S("Version")) > UpdateService.CurrentVersion)
                {
                    _updateStatus.Text = Localization.T("上次安装未完成，请查看更新日志后重试。");
                    Notice(_updateStatus.Text, true);
                }
            }
        }
        catch (Exception) { /* Unreadable result files do not block the app. */ }
    }
    void ShowRelease()
    {
        _releaseNotes.Text = _release?.Notes ?? "";
        _releaseNotes.Visibility = _release == null ? Visibility.Collapsed : Visibility.Visible;
        UpdateButtons();
    }
    async Task CheckUpdatesAsync(bool manual)
    {
        if (_updateBusy || _closing || IsVerification) return;
        _updates ??= new UpdateService(Client.Runtime);
        _updateBusy = true; UpdateButtons();
        using var operation = CancellationTokenSource.CreateLinkedTokenSource(_updateLifetime.Token);
        _updateOperation = operation;
        try
        {
            Directory.CreateDirectory(Path.Combine(Client.Runtime, "updates"));
            // Shared across desktop instances: a restart cannot bypass daily checks.
            using var lease = new FileStream(Path.Combine(Client.Runtime, "updates", "check.lock"), FileMode.OpenOrCreate, FileAccess.ReadWrite, FileShare.None);
            var state = _updates.LoadState();
            if (!manual && !UpdateService.IsDue(state, DateTimeOffset.UtcNow)) return;
            await _updates.SaveStateAsync(state with { LastAttempt = DateTimeOffset.UtcNow });
            _updateStatus.Text = Localization.T("正在检查更新…");
            var release = await _updates.CheckAsync(operation.Token);
            if (_closing) return;
            if (release == null)
            {
                _updateStatus.Text = Localization.T("当前没有可用的新稳定版。");
                return;
            }
            if (!manual && state.SkippedVersion == release.Version.ToString())
            {
                _updateStatus.Text = Localization.T("已跳过此版本，仍可手动检查。");
                return;
            }
            if (_release?.Tag != release.Tag) _updatePackage = null;
            _release = release; ShowRelease();
            _updateStatus.Text = Localization.F($"发现新版本：{release.Version}");
            Notice(_updateStatus.Text);
            if (UpdateDialog.Choose(this, release) is { } choice)
            {
                if (choice == "skip") { await SkipUpdateAsync(); return; }
                if (choice == "download")
                {
                    _updateBusy = false;
                    await DownloadUpdateAsync();
                }
            }
        }
        catch (OperationCanceledException) { _updateStatus.Text = Localization.T("更新操作已取消，当前版本保留。"); }
        catch (Exception ex)
        {
            if (manual) _updateStatus.Text = Localization.T("检查更新失败，请重试。") + " " + SafeText.Clean(ex.Message);
            else _updateStatus.Text = Localization.T("自动检查暂不可用，可手动重试。");
        }
        finally { if (ReferenceEquals(_updateOperation, operation)) _updateOperation = null; _updateBusy = false; UpdateButtons(); }
    }
    async Task SkipUpdateAsync()
    {
        if (_release == null || _updates == null) return;
        await _updates.SaveStateAsync(_updates.LoadState() with { SkippedVersion = _release.Version.ToString() });
        _updateStatus.Text = Localization.T("已跳过此版本，仍可手动检查。");
    }
    async Task DownloadUpdateAsync()
    {
        if (_updateBusy || _release == null || _updates == null) return;
        // Choosing Download in the release dialog or clicking this button is consent.
        _updateBusy = true; UpdateButtons();
        _updateOperation = CancellationTokenSource.CreateLinkedTokenSource(_updateLifetime.Token);
        bool installNext = false;
        try
        {
            _updateStatus.Text = Localization.T("正在下载更新…");
            var progress = new Progress<double>(value => _updateStatus.Text = Localization.F($"正在下载更新：{value:P0}"));
            _updatePackage = await _updates.DownloadAsync(_release, progress, _updateOperation.Token);
            await _updates.SavePendingAsync(_release, _updatePackage);
            _updateStatus.Text = Localization.T("更新已下载，可稍后重启安装。");
            if (!_closing && MessageBox.Show(this, Localization.T("更新已下载。现在等待任务结束并重启安装？选择否则稍后安装。"), Localization.T("软件更新"), MessageBoxButton.YesNo) == MessageBoxResult.Yes)
            {
                installNext = true;
            }
        }
        catch (OperationCanceledException) { _updateStatus.Text = Localization.T("更新操作已取消，当前版本保留。"); }
        catch (Exception ex) { _updateStatus.Text = Localization.T("下载更新失败，请重试。") + " " + SafeText.Clean(ex.Message); }
        finally { _updateOperation?.Dispose(); _updateOperation = null; _updateBusy = false; UpdateButtons(); }
        if (installNext) await InstallUpdateAsync(true);
    }
    async Task InstallUpdateAsync(bool confirmed = false)
    {
        if (_updateBusy || _release == null || _updatePackage == null || _exitPending || _commandPending) return;
        if (!AppPaths.Installed(Client.Root))
        {
            _updateStatus.Text = Localization.T("当前为开发版目录；安装包完成后，安装版才能自动升级。");
            return;
        }
        if (!confirmed && !Confirm(Localization.T("重启并安装更新"), Localization.T("将暂停新任务并等待现有任务完成，然后退出并安装。配置和用户资料保留。"))) return;
        _updateBusy = true;
        _updateOperation = CancellationTokenSource.CreateLinkedTokenSource(_updateLifetime.Token);
        bool paused = false, stopped = false;
        UpdateButtons();
        try
        {
            await UpdateService.VerifyPackageAsync(_updatePackage, _release, _updateOperation.Token);
            // Do not hold _work while draining: task review/approval stays usable.
            var status = await Client.SendAsync(new() { ["Action"] = "status" }, _updateOperation.Token); Check(status);
            if (_launcher is { IsCompleted: false } && !status.B("Online")) throw new InvalidOperationException(Localization.T("服务正在启动，请稍后重试。"));
            if (status.B("Online"))
            {
                var roster = await Client.SendAsync(new() { ["Action"] = "list" }, _updateOperation.Token); Check(roster);
                // Mark before sending: even an uncertain ACK must be followed by resume.
                paused = true;
                Check(await Client.SendAsync(new() { ["Action"] = "update-pause", ["Version"] = roster.S("Version") }, _updateOperation.Token));
                _updateStatus.Text = Localization.T("等待现有任务完成；可取消更新等待。");
                while (true)
                {
                    _updateOperation.Token.ThrowIfCancellationRequested();
                    roster = await Client.SendAsync(new() { ["Action"] = "list" }, _updateOperation.Token); Check(roster);
                    var reply = await Client.SendAsync(new() { ["Action"] = "update-stop", ["Version"] = roster.S("Version") }, _updateOperation.Token);
                    if (reply.B("Success")) { stopped = true; break; }
                    if (reply.S("Code") != "busy") Check(reply);
                    await Task.Delay(1000, _updateOperation.Token);
                }
            }
            _updateInstalling = true; UpdateButtons();
            _updateStatus.Text = Localization.T("正在等待服务退出并准备安装…");
            // The independent helper rechecks all process locks after this desktop exits.
            await LaunchUpdateHelperAsync(_updatePackage, _release);
            _closing = true; Close();
        }
        catch (Exception ex)
        {
            _updateStatus.Text = ex is OperationCanceledException ? Localization.T("更新操作已取消，当前版本保留。") : SafeText.Clean(ex.Message);
        }
        finally
        {
            if (paused && !stopped)
            {
                try
                {
                    var roster = await Client.SendAsync(new() { ["Action"] = "list" }); Check(roster);
                    Check(await Client.SendAsync(new() { ["Action"] = "update-resume", ["Version"] = roster.S("Version") }));
                }
                catch { Notice(Localization.T("恢复接收任务未确认，请检查服务状态。"), true); }
            }
            _updateOperation?.Dispose(); _updateOperation = null;
            _updateBusy = false; _updateInstalling = false; UpdateButtons();
        }
    }
    async Task LaunchUpdateHelperAsync(string package, UpdateRelease release)
    {
        var helperDirectory = Path.Combine(Client.Runtime, "updates", "handoff-" + Guid.NewGuid().ToString("N"));
        Directory.CreateDirectory(helperDirectory);
        var helper = Path.Combine(helperDirectory, "Install-Update.ps1");
        File.Copy(Path.Combine(Client.Root, "Install-Update.ps1"), helper);
        var start = new ProcessStartInfo(LocalClient.Shell) { UseShellExecute = false, CreateNoWindow = true, WorkingDirectory = helperDirectory };
        foreach (var arg in new[] { "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-File", helper, "-ProjectRoot", Client.Root, "-Runtime", Client.Runtime, "-Package", package, "-Sha256", release.Sha256, "-Version", release.Version.ToString(), "-ParentId", Environment.ProcessId.ToString(), "-ReadyFile", Path.Combine(helperDirectory, "ready") }) start.ArgumentList.Add(arg);
        using var process = Process.Start(start) ?? throw new InvalidOperationException("Could not start update helper.");
        for (int i = 0; i < 1200; i++)
        {
            if (process.HasExited) throw new InvalidOperationException(Localization.T("更新准备失败，当前版本保留。"));
            if (File.Exists(Path.Combine(helperDirectory, "ready"))) return;
            await Task.Delay(100);
        }
        process.Kill();
        throw new InvalidOperationException(Localization.T("更新准备失败，当前版本保留。"));
    }
}

internal static class UpdateDialog
{
    internal static string? Choose(Window owner, UpdateRelease release)
    {
        string? result = null;
        var window = new Window { Owner = owner, Title = Localization.T("软件更新"), Width = 620, SizeToContent = SizeToContent.Height, MaxHeight = 620, WindowStartupLocation = WindowStartupLocation.CenterOwner };
        var stack = new StackPanel { Margin = new Thickness(24) };
        stack.Children.Add(new TextBlock { Text = Localization.F($"发现新版本：{release.Version}"), FontSize = 20 });
        stack.Children.Add(new TextBox { Text = release.Notes, IsReadOnly = true, TextWrapping = TextWrapping.Wrap, MaxHeight = 350, VerticalScrollBarVisibility = ScrollBarVisibility.Auto });
        var row = new WrapPanel();
        foreach (var item in new[] { ("下载更新", "download"), ("稍后提醒", "later"), ("跳过此版本", "skip") })
        {
            var button = new Button { Content = Localization.T(item.Item1) };
            button.Click += (_, _) => { result = item.Item2; window.DialogResult = true; };
            row.Children.Add(button);
        }
        stack.Children.Add(row); WindowCaption.Attach(window, stack); window.ShowDialog(); return result;
    }
}
