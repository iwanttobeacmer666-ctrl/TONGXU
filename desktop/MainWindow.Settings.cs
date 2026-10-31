using System.IO;
using System.Text.Json.Nodes;
using System.Windows;
using System.Windows.Controls;
using System.Windows.Media;
using System.Windows.Media.Imaging;
using Microsoft.Win32;

namespace Tongxu.Desktop;

public partial class MainWindow
{
    TextBlock _serviceState = null!, _platformState = null!, _agentState = null!, _credentialState = null!, _pathsText = null!, _backgroundText = null!;
    Button _startButton = null!, _stopButton = null!; string _background = "";
    ComboBox _language = null!, _theme = null!;
    bool _themeChanging;
    bool _languageChanging;
    FrameworkElement BuildConnection()
    {
        var page = Page("连接与运行", "本地服务、企业微信连接和 Agent 可用状态分别展示。");
        var stack = V();
        Put(page, Scroll(stack), 1);
        var statuses = new Grid { Margin = new Thickness(0, 14, 0, 26) };
        for (int i = 0; i < 3; i++)
            statuses.ColumnDefinitions.Add(new());
        var labels = new[] { "本地服务", "企业微信", "Codex CLI" };
        var values = new List<TextBlock>();
        for (int i = 0; i < 3; i++)
        {
            var col = V();
            col.Margin = new Thickness(0, 0, 20, 0);
            col.Children.Add(T(labels[i], 12, false, true));
            var value = T("正在读取…", 24, true);
            values.Add(value);
            col.Children.Add(value);
            Grid.SetColumn(col, i);
            statuses.Children.Add(col);
        }
        (_serviceState, _platformState, _agentState) = (values[0], values[1], values[2]);
        stack.Children.Add(statuses);
        stack.Children.Add(T("连接配置", 17, true));
        var selectors = Flow();
        selectors.Children.Add(Field("消息平台", AdapterChoice("wecom", "企业微信"), 240));
        selectors.Children.Add(Field("Agent", AdapterChoice("codex", "Codex CLI"), 240));
        stack.Children.Add(selectors);
        _credentialState = T("凭据默认隐藏；桌面启动不会自动连接群。", 12, false, true);
        stack.Children.Add(_credentialState);
        var actions = Flow();
        actions.Margin = new Thickness(0, 20, 0, 22);
        _startButton = B("启动服务", StartServiceAsync, "Primary");
        _stopButton = B("停止服务", StopServiceAsync, "Danger");
        actions.Children.Add(_startButton);
        actions.Children.Add(_stopButton);
        actions.Children.Add(B("连接配置", () => EditConfigurationAsync(false)));
        actions.Children.Add(B("专用 Codex 登录", () => { if (Confirm(Localization.T("打开专用 Codex 登录"), Localization.T("将打开独立登录窗口，仅管理此项目的专用 Codex 登录。不会启动群服务。"))) Client.Login(); return Task.CompletedTask; }));
        actions.Children.Add(B("刷新状态", () => RefreshCoreAsync(true)));
        stack.Children.Add(actions);
        stack.Children.Add(T("启动前会检查现有配置和 Codex 程序；启动后平台连接仍可能处于连接中。\nAgent 就绪表示程序已通过启动检查，登录和真实模型请求尚需验证。", 12, false, true));
        stack.Children.Add(T("异常与清理", 17, true));
        stack.Children.Add(T("服务异常退出不会自动重新启动。停止请求被接受后，需等待本地服务确实退出；临时清理失败不会被显示为删除成功。", 13, false, true));
        return page;
    }
    void UpdateConnection()
    {
        var state = _status.S("Service");
        _serviceState.Text = !_serviceKnown ? Localization.T("暂不可确认") : _online ? (state == "stopping" ? Localization.T("停止中") : Localization.T("运行中")) : _launcher is { IsCompleted: false } ? Localization.T("启动检查中") : Localization.T("未启动");
        _platformState.Text = !_serviceKnown ? Localization.T("暂不可确认") : _status.S("Platform") switch
        {
            "connected" => Localization.T("已连接"),
            "connecting" => Localization.T("连接 / 重试中"),
            "unknown" => Localization.T("暂不可确认"),
            _ => Localization.T("未连接")
        };
        _agentState.Text = !_serviceKnown ? Localization.T("暂不可确认") : _status.S("Agent") switch
        {
            "ready" => Localization.T("本地就绪"),
            "unavailable" => Localization.T("不可用"),
            _ => _settings.B("AgentFileExists") ? Localization.T("待启动检查") : Localization.T("程序不可用")
        };
        _credentialState.Text = Localization.F($"连接程序：企业微信  ·  Agent：Codex CLI\n机器人 ID：{_settings.S("BotIDMasked")}  ·  凭据：{(_settings.B("HasCredentials") ? Localization.T("已加密保存（不显示）") : Localization.T("尚未配置"))}\n当前管理员：{_owner}");
        _startButton.IsEnabled = _serviceKnown && !_online && _launcher is not { IsCompleted: false } && _settings.B("HasCredentials");
        _stopButton.IsEnabled = _serviceKnown && _online && state != "stopping";
        UpdateButtons();
        if (_status.B("UpdatePaused") && !_updateBusy) _updateStatus.Text = Localization.T("等待现有任务完成；可取消更新等待。");
    }
    async Task StartServiceAsync()
    {
        if (_online || _launcher is { IsCompleted: false })
            return;
        if (!Confirm(Localization.T("启动企业微信群服务"), Localization.T("将运行现有后端并连接企业微信。各群已启用成员可开始任务。\n不会执行历史清理或搬迁数据。")))
            return;
        _launcher = Client.LaunchAsync();
        UpdateConnection();
        Notice(Localization.T("正在校验配置及 Codex 权限；尚未确认平台连接。版本或检查失败不会自动修复。"));
        _ = ObserveLauncherAsync(_launcher);
        await Task.CompletedTask;
    }
    async Task ObserveLauncherAsync(Task<JsonNode> task)
    {
        try
        {
            var reply = await task;
            Notice(reply.S("Message"), !reply.B("Success"));
            if (!_closing)
                await RefreshAsync(true);
        }
        catch (Exception) { Notice(Localization.T("服务启动或退出未确认，请刷新核对。"), true); }
    }
    async Task StopServiceAsync()
    {
        if (!_online)
            return;
        if (!Confirm(Localization.T("停止群服务"), Localization.T("将停止接收全部群的新消息，并通过现有退出流程取消任务。\n配置、权限、文档、依赖和登录凭据保留。停止请求不代表临时清理已完成。")))
            return;
        var reply = await Client.SendAsync(new()
        {
            ["Action"] = "service-stop",
            ["Version"] = _version
        });
        Check(reply);
        _status = reply["Status"]?.DeepClone();
        UpdateConnection();
        Notice(reply.S("Message"));
    }
    FrameworkElement BuildSettings()
    {
        var page = Page("设置", "桌面外观立即生效；目录设置在下次启动服务生效。");
        var stack = V();
        Put(page, Scroll(stack), 1);
        stack.Children.Add(T("外观", 17, true));
        _theme = new ComboBox();
        UpdateThemeChoices();
        _theme.SelectionChanged += async (_, _) =>
        {
            if (_themeChanging || !_ready) return;
            await RunAsync(ChangeThemeAsync);
        };
        stack.Children.Add(Field("颜色模式", _theme, 380));
        _language = new ComboBox();
        Choices(_language, Localization.Languages);
        _language.SelectedItem = Localization.Languages.First(l => l.ID == Localization.Current);
        _language.SelectionChanged += async (_, _) =>
        {
            if (_languageChanging || !_ready)
                return;
            await RunAsync(ChangeLanguageAsync);
        };
        stack.Children.Add(Field("界面语言", _language, 380));
        stack.Children.Add(T("界面语言立即生效；任务输出和文件内容保留原文。", 12, false, true));
        _backgroundText = T("默认背景", 12, false, true);
        stack.Children.Add(_backgroundText);
        var bg = Flow();
        bg.Children.Add(B("选择背景图", ChooseBackgroundAsync));
        bg.Children.Add(B("恢复默认", async () => { _background = ""; Surface.Background = Theme.Get("Surface"); await SavePreferencesAsync(); _backgroundText.Text = Localization.T("默认背景 · 立即生效"); }));
        stack.Children.Add(bg);
        stack.Children.Add(T("服务目录", 17, true));
        _pathsText = T("读取现有配置中…", 13);
        stack.Children.Add(_pathsText);
        stack.Children.Add(T("不自动搬迁、复制或删除原目录中的资料。编辑前需停止服务，保存时复用现有路径校验。\n更换目录后，旧资料和会话仍在原位置；新位置不会自动继承。", 12, false, true));
        stack.Children.Add(B("编辑服务目录", () => EditConfigurationAsync(true)));
        AddUpdateSettings(stack);
        return page;
    }
    void UpdateSettingsView()
    {
        _updateVersionText.Text = Localization.F($"当前版本：{UpdateService.CurrentVersion}");
        var paths = _settings?["Paths"];
        _pathsText.Text = string.Join("\n\n", new[] { (Localization.T("文档目录"), "DataDir"), (Localization.T("任务临时目录"), "WorkDir"), (Localization.T("用户依赖目录"), "SoftwareDir"), (Localization.T("会话状态目录"), "StateDir"), (Localization.T("专用 Codex 登录目录"), "CodexHome"), (Localization.T("Codex CLI 程序"), "CodexCommand") }.Select(p => p.Item1 + "\n" + paths.S(p.Item2)));
        if (!_ready)
            LoadPreferences();
        _backgroundText.Text = _background == "" ? Localization.T("默认背景 · 立即生效") : Localization.T("背景图：") + Path.GetFileName(_background) + Localization.T(" · 立即生效");
    }
    async Task ChangeLanguageAsync()
    {
        var code = Selected(_language);
        if (code == Localization.Current || !Localization.Languages.Any(l => l.ID == code))
            return;
        _languageChanging = true;
        try
        {
            // Persist first. A failed write leaves both the UI and saved choice unchanged.
            await SavePreferencesAsync(code);
            Localization.SetLanguage(code);
            UpdateThemeChoices();
            Localization.ApplyTo(this);
            UpdateRosterViews();
            foreach (var row in _members.Concat(_disabled).Concat(_tasks).Concat(_documents))
                row.RefreshLanguage();
            Choices(_taskState, new[] { new Choice("", Localization.T("全部状态")) }.Concat(new[] { "downloading", "queued", "running", "waiting_approval", "publishing", "stopping", "cleanup_failed", "stopped", "completed", "failed" }.Select(s => new Choice(s, J.State(s)))));
            UpdateSettingsView();
            UpdateConnection();
            // Refresh only display labels. The backend, current page, filters,
            // selected rows and task output are not restarted or reconstructed.
            UpdateTaskInfo();
            UpdateDocumentLabels();
            Notice(Localization.T("界面语言已更新，立即生效。"));
        }
        finally
        {
            _language.SelectedItem = Localization.Languages.First(l => l.ID == Localization.Current);
            _languageChanging = false;
        }
    }
    async Task EditConfigurationAsync(bool paths)
    {
        if (_online)
            throw new InvalidOperationException(Localization.T("请先停止服务，再修改连接或目录配置。群与成员在线管理不受此限制。"));
        var current = await Client.SettingsAsync("read");
        Check(current);
        var dialog = new ConfigurationWindow(Client, current, paths) { Owner = this };
        if (dialog.ShowDialog() != true || dialog.SavedReply == null)
            return;
        _settings = null;
        await RefreshCoreAsync(true);
        Notice(dialog.SavedReply.S("Message"));
    }
    async Task ChooseBackgroundAsync()
    {
        var picker = new OpenFileDialog { Filter = Localization.T("图片|*.png;*.jpg;*.jpeg;*.bmp") };
        if (picker.ShowDialog(this) != true)
            return;
        ApplyBackground(picker.FileName);
        _background = picker.FileName;
        await SavePreferencesAsync();
        _backgroundText.Text = Localization.T("背景图：") + Path.GetFileName(_background) + Localization.T(" · 立即生效");
    }
    void ApplyBackground(string path)
    {
        if (new FileInfo(path).Length > 20 << 20)
            throw new InvalidOperationException(Localization.T("背景图请控制在 20 MB 以内。"));
        var image = new BitmapImage();
        image.BeginInit();
        image.CacheOption = BitmapCacheOption.OnLoad;
        image.DecodePixelWidth = 1600;
        image.UriSource = new Uri(path);
        image.EndInit();
        image.Freeze();
        Surface.Background = new ImageBrush(image) { Stretch = Stretch.UniformToFill, Opacity = .6 };
    }
    internal static ComboBox AdapterChoice(string id, string label)
    {
        var box = new ComboBox();
        Choices(box, [new Choice(id, label)]);
        return box;
    }
    void UpdateThemeChoices()
    {
        _themeChanging = true;
        try
        {
            Choices(_theme, [new("system", Localization.T("跟随系统")), new("light", Localization.T("浅色")), new("dark", Localization.T("深色"))]);
            _theme.SelectedItem = ((IEnumerable<Choice>)_theme.ItemsSource).First(c => c.ID == Theme.Mode);
        }
        finally { _themeChanging = false; }
    }
    async Task ChangeThemeAsync()
    {
        var mode = Selected(_theme);
        try
        {
            await SavePreferencesAsync(theme: mode);
            Theme.Apply(mode);
        }
        finally { UpdateThemeChoices(); }
    }
    void OnSystemThemeChanged(object sender, UserPreferenceChangedEventArgs e)
    {
        if (!Dispatcher.HasShutdownStarted)
            Dispatcher.BeginInvoke(() => { if (!_closing && Theme.Mode == "system") Theme.Apply("system"); });
    }
    string Preferences => Path.Combine(Client.Runtime, "desktop-settings.json");
    void LoadPreferences()
    {
        try
        {
            if (!File.Exists(Preferences))
            {
                Theme.Apply("system");
                UpdateThemeChoices();
                Surface.Background = Theme.Get("Surface");
                return;
            }
            if (new FileInfo(Preferences).Length > 16384)
                throw new InvalidOperationException();
            var value = JsonNode.Parse(File.ReadAllText(Preferences));
            Theme.Apply(value.S("Theme"));
            UpdateThemeChoices();
            _background = value.S("BackgroundImage");
            Surface.Background = Theme.Get("Surface");
            if (_background != "")
            {
                ApplyBackground(_background);
                _backgroundText.Text = Localization.T("背景图：") + Path.GetFileName(_background) + Localization.T(" · 立即生效");
            }
        }
        catch { _background = ""; Theme.Apply("system"); UpdateThemeChoices(); Surface.Background = Theme.Get("Surface"); Notice(Localization.T("背景设置暂不可读取，已使用默认外观；原设置文件保留。"), true); }
    }
    async Task SavePreferencesAsync(string? language = null, string? theme = null)
    {
        Directory.CreateDirectory(Path.GetDirectoryName(Preferences)!);
        var temp = Preferences + "." + Guid.NewGuid().ToString("N") + ".tmp";
        try
        {
            await File.WriteAllTextAsync(temp, new JsonObject { ["Language"] = language ?? Localization.Current, ["Theme"] = theme ?? Theme.Mode, ["BackgroundImage"] = _background }.ToJsonString());
            File.Move(temp, Preferences, true);
        }
        finally { if (File.Exists(temp)) File.Delete(temp); }
    }
}
