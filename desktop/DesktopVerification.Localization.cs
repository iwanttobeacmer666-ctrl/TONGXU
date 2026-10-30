using System.Globalization;
using System.IO;
using System.Text;
using System.Text.Json.Nodes;
using System.Windows;
using System.Windows.Automation;
using System.Windows.Controls;
using System.Windows.Media;
using System.Windows.Shell;
using System.Windows.Media.Imaging;
using System.Windows.Threading;

namespace Tongxu.Desktop;

public partial class MainWindow
{
    internal async Task VerifyLanguageAndCaptionAsync(string evidence)
    {
        var keys = Localization.Messages["zh-CN"].Keys.ToHashSet(StringComparer.Ordinal);
        if (Localization.Languages.Length != 10 || Localization.Messages.Count != 10)
            throw new InvalidOperationException("Expected ten complete interface languages.");
        foreach (var language in Localization.Messages)
        {
            if (!keys.SetEquals(language.Value.Keys) || language.Value.Values.Any(string.IsNullOrWhiteSpace))
                throw new InvalidOperationException("Incomplete language: " + language.Key);
            foreach (var message in language.Value)
                if (CompositeFormat.Parse(message.Key).MinimumArgumentCount != CompositeFormat.Parse(message.Value).MinimumArgumentCount)
                    throw new InvalidOperationException("Translation format mismatch: " + language.Key);
        }
        if (WindowStyle != WindowStyle.None || WindowChrome.GetWindowChrome(this) is not { CaptionHeight: WindowCaption.Height, GlassFrameThickness.Top: 0 })
            throw new InvalidOperationException("Native white title bar still active.");

        await NavigateAsync(1);
        _memberSearch.Text = "long-user-id";
        _memberTable.SelectedItem = _memberTable.Items.OfType<ViewRow>().Single();
        var member = _memberTable.SelectedItem;
        _disabledSearch.Text = "bob";
        _taskUser.Text = "alice";
        _docSearch.Text = "large";
        await NavigateAsync(3);
        var task = _taskTable.SelectedItem;
        _eventOutput.ScrollToVerticalOffset(100);
        var output = _eventOutput.Text;
        var scroll = _eventOutput.VerticalOffset;
        var memberGroup = Selected(_memberGroup);
        var fileGroup = Selected(_docGroup);
        var fileScope = Selected(_docScope);
        var raw = new ViewRow { Data = new JsonObject { ["User"] = "设置", ["Name"] = "公共文件", ["State"] = "cleanup_failed" } };
        await NavigateAsync(5);
        await Dispatcher.Yield(DispatcherPriority.ContextIdle);
        scroll = _eventOutput.VerticalOffset;
        var originalBackground = _background;
        var headings = new[] { "连接与运行", "群与成员", "已停用用户", "任务审查", "文档管理", "设置" };
        foreach (var language in Localization.Languages)
        {
            _languageChanging = true;
            _language.SelectedItem = language;
            _languageChanging = false;
            await RunAsync(ChangeLanguageAsync);
            await Dispatcher.Yield(DispatcherPriority.ContextIdle);
            var expectedBrand = language.ID.StartsWith("zh-", StringComparison.Ordinal) ? "同序" : "tongxu";
            if (_brandLabel.Text != expectedBrand || Title != Localization.T("同序 · 工作台"))
                throw new InvalidOperationException("Product name did not update consistently: " + language.ID);
            if (Localization.Current != language.ID || _nav.SelectedIndex != 5 ||
                !ReferenceEquals(member, _memberTable.SelectedItem) || !ReferenceEquals(task, _taskTable.SelectedItem) ||
                _memberSearch.Text != "long-user-id" || _disabledSearch.Text != "bob" || _taskUser.Text != "alice" || _docSearch.Text != "large" ||
                Selected(_memberGroup) != memberGroup || Selected(_docGroup) != fileGroup || Selected(_docScope) != fileScope ||
                _eventOutput.Text != output || Math.Abs(_eventOutput.VerticalOffset - scroll) > 1 || _background != originalBackground)
                throw new InvalidOperationException("Language switch reset current state: " + language.ID + " " + new JsonObject { ["Current"] = Localization.Current, ["Page"] = _nav.SelectedIndex, ["Member"] = ReferenceEquals(member, _memberTable.SelectedItem), ["Task"] = ReferenceEquals(task, _taskTable.SelectedItem), ["MemberSearch"] = _memberSearch.Text, ["DisabledSearch"] = _disabledSearch.Text, ["TaskUser"] = _taskUser.Text, ["FileSearch"] = _docSearch.Text, ["MemberGroup"] = Selected(_memberGroup) == memberGroup, ["FileGroup"] = Selected(_docGroup) == fileGroup, ["FileScope"] = Selected(_docScope) == fileScope, ["Output"] = _eventOutput.Text == output, ["ScrollExpected"] = scroll, ["ScrollActual"] = _eventOutput.VerticalOffset, ["Background"] = _background == originalBackground }.ToJsonString());
            if (raw.User != "设置" || raw.Name != "公共文件" || raw.Status != Localization.T("清理失败"))
                throw new InvalidOperationException("Language switch changed user data.");
            var saved = JsonNode.Parse(await File.ReadAllTextAsync(Preferences));
            if (saved.S("Language") != language.ID || saved.S("BackgroundImage") != originalBackground)
                throw new InvalidOperationException("Language preference was not persisted.");
            for (int page = 0; page < _pages.Count; page++)
            {
                _content.Content = _pages[page]; // Rendering only, no new backend operations.
                await Dispatcher.Yield(DispatcherPriority.ContextIdle);
                DesktopVerification.Capture(this, Path.Combine(evidence, $"locale-{language.ID}-{page + 1}.png"), 1040, 740);
                if (!VisibleText(_pages[page]).Contains(Localization.T(headings[page]), StringComparison.Ordinal))
                    throw new InvalidOperationException("Page heading did not update: " + language.ID + "/" + page);
            }
            _content.Content = _pages[5];
        }
        var preferenceBytes = await File.ReadAllBytesAsync(Preferences);
        using (var held = new FileStream(Preferences, FileMode.Open, FileAccess.Read, FileShare.Read))
        {
            _languageChanging = true;
            _language.SelectedItem = Localization.Languages.First(l => l.ID == "fr");
            _languageChanging = false;
            await RunAsync(ChangeLanguageAsync);
            if (Localization.Current != "ru" || Selected(_language) != "ru" || !(await File.ReadAllBytesAsync(Preferences)).SequenceEqual(preferenceBytes))
                throw new InvalidOperationException("A failed preference save changed the language or overwrote existing settings.");
        }
        Localization.SetLanguage("zh-CN");
        Localization.LoadPreference(Client.Root);
        if (Localization.Current != "ru")
            throw new InvalidOperationException("Persisted language was not restored.");
        try
        {
            await File.WriteAllTextAsync(Preferences, "[]");
            Localization.LoadPreference(Client.Root);
            if (Localization.Current != "ru")
                throw new InvalidOperationException("Malformed appearance settings changed the current language.");
        }
        finally { await File.WriteAllBytesAsync(Preferences, preferenceBytes); }
        var reopened = new MainWindow(Client) { IsVerification = true };
        await reopened.InitializeAsync();
        if (!reopened.Title.Contains(Localization.T("工作台"), StringComparison.Ordinal))
            throw new InvalidOperationException("Language was not applied to a newly created window.");
        reopened.Close();
        _languageChanging = true;
        _language.SelectedItem = Localization.Languages.First();
        _languageChanging = false;
        await RunAsync(ChangeLanguageAsync);
        _memberSearch.Text = "";
        _disabledSearch.Text = "";
        _taskUser.Text = "";
        _docSearch.Text = "";
        await Dispatcher.Yield(DispatcherPriority.ContextIdle);
        await VerifyNativeCaptionAsync();
        await VerifyCaptionBackgroundAsync(evidence);
    }

    async Task VerifyNativeCaptionAsync()
    {
        var probe = new Window { Title = "Isolated window controls check", Width = 600, Height = 400, Left = -10000, Top = -10000, ShowActivated = false, ShowInTaskbar = false, WindowStartupLocation = WindowStartupLocation.Manual };
        WindowCaption.Attach(probe, new Grid());
        bool closed = false;
        probe.Closed += (_, _) => closed = true;
        try
        {
            probe.Show();
            await Dispatcher.Yield(DispatcherPriority.ContextIdle);
            IconVerification.CheckNative(probe);
            Button Find(DependencyObject node, string name)
            {
                if (node is Button button && AutomationProperties.GetName(button) == Localization.T(name))
                    return button;
                for (int i = 0; i < VisualTreeHelper.GetChildrenCount(node); i++)
                {
                    var found = Find(VisualTreeHelper.GetChild(node, i), name);
                    if (found != null)
                        return found;
                }
                return null!;
            }
            async Task Click(string name)
            {
                var button = Find(probe, name) ?? throw new InvalidOperationException("Caption button unavailable.");
                if (!WindowChrome.GetIsHitTestVisibleInChrome(button))
                    throw new InvalidOperationException("Caption control not interactive.");
                button.RaiseEvent(new RoutedEventArgs(Button.ClickEvent));
                await Dispatcher.Yield(DispatcherPriority.ContextIdle);
            }
            await Click("最大化");
            if (probe.WindowState != WindowState.Maximized)
                throw new InvalidOperationException("Maximize failed.");
            await Click("还原窗口");
            if (probe.WindowState != WindowState.Normal)
                throw new InvalidOperationException("Restore failed.");
            await Click("最小化");
            if (probe.WindowState != WindowState.Minimized)
                throw new InvalidOperationException("Minimize failed.");
            SystemCommands.RestoreWindow(probe);
            await Dispatcher.Yield(DispatcherPriority.ContextIdle);
            await Click("关闭");
            if (!closed)
                throw new InvalidOperationException("Close failed.");
        }
        finally { if (!closed) probe.Close(); }
    }

    async Task VerifyCaptionBackgroundAsync(string evidence)
    {
        var sample = new DrawingVisual();
        using (var drawing = sample.RenderOpen())
            drawing.DrawRectangle(new SolidColorBrush(Color.FromRgb(40, 100, 70)), null, new Rect(0, 0, 320, 180));
        var bitmap = new RenderTargetBitmap(320, 180, 96, 96, PixelFormats.Pbgra32);
        bitmap.Render(sample);
        var path = Path.Combine(evidence, "isolated-background.png");
        var png = new PngBitmapEncoder();
        png.Frames.Add(BitmapFrame.Create(bitmap));
        using (var stream = File.Create(path))
            png.Save(stream);
        ApplyBackground(path);
        DesktopVerification.Capture(this, Path.Combine(evidence, "desktop-blended-background.png"), 1040, 740);
        var rendered = new RenderTargetBitmap(1040, 740, 96, 96, PixelFormats.Pbgra32);
        rendered.Render(Surface);
        byte[] Pixel(int x, int y)
        {
            var bytes = new byte[4];
            rendered.CopyPixels(new Int32Rect(x, y, 1, 1), bytes, 4, 0);
            return bytes;
        }
        if (!Pixel(730, 20).SequenceEqual(Pixel(730, 44)) || !Pixel(10, 20).SequenceEqual(Pixel(10, 44)))
            throw new InvalidOperationException("Caption/background seam detected.");
        Surface.Background = Theme.Get("Surface");
        await Task.CompletedTask;
    }

    static string VisibleText(DependencyObject node)
    {
        var text = node is TextBlock block ? block.Text : "";
        for (int i = 0; i < VisualTreeHelper.GetChildrenCount(node); i++)
            text += "\n" + VisibleText(VisualTreeHelper.GetChild(node, i));
        return text;
    }
}
