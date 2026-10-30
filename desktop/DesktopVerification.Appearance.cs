using System.IO;
using System.Text.Json.Nodes;
using System.Windows;
using System.Windows.Controls;
using System.Windows.Media;
using System.Windows.Threading;

namespace Tongxu.Desktop;

public partial class MainWindow
{
    internal async Task VerifyThemeAndTrayAsync(string evidence)
    {
        await NavigateAsync(5);
        async Task SelectTheme(string mode)
        {
            _themeChanging = true;
            _theme.SelectedItem = ((IEnumerable<Choice>)_theme.ItemsSource).Single(c => c.ID == mode);
            _themeChanging = false;
            await RunAsync(ChangeThemeAsync);
            if (Theme.Mode != mode || JsonNode.Parse(await File.ReadAllTextAsync(Preferences)).S("Theme") != mode)
                throw new InvalidOperationException("Theme not applied/persisted: " + mode);
        }
        await SelectTheme("dark");
        if (!Theme.IsDark || _brandLabel.Foreground != Theme.Get("Ink") || _memberTable.Background != Theme.Get("Surface"))
            throw new InvalidOperationException("Theme did not reach text/table surfaces.");
        for (int page = 0; page < 6; page++)
        {
            _content.Content = _pages[page];
            await Dispatcher.Yield(DispatcherPriority.ContextIdle);
            DesktopVerification.Capture(this, Path.Combine(evidence, $"dark-{page + 1}.png"), 1040, 740);
        }
        if (_nav.Items.OfType<TextBlock>().Any(label => label.Foreground != Theme.Get("Ink")))
            throw new InvalidOperationException("Navigation labels did not inherit active theme.");
        var config = new ConfigurationWindow(Client, _settings!, false);
        if (config.Background != Theme.Get("Surface") || config.Foreground != Theme.Get("Ink"))
            throw new InvalidOperationException("Dialog did not inherit active theme.");
        DesktopVerification.CaptureElement((FrameworkElement)config.Content, Path.Combine(evidence, "dark-configuration.png"), 724, 590);
        config.Close();
        await SelectTheme("light");
        if (Theme.IsDark) throw new InvalidOperationException("Explicit light theme failed.");
        var preferenceBytes = await File.ReadAllBytesAsync(Preferences);
        using (var held = new FileStream(Preferences, FileMode.Open, FileAccess.Read, FileShare.Read))
        {
            _themeChanging = true;
            _theme.SelectedItem = ((IEnumerable<Choice>)_theme.ItemsSource).Single(c => c.ID == "dark");
            _themeChanging = false;
            await RunAsync(ChangeThemeAsync);
            if (Theme.Mode != "light" || Selected(_theme) != "light" || !(await File.ReadAllBytesAsync(Preferences)).SequenceEqual(preferenceBytes))
                throw new InvalidOperationException("Failed theme save changed preference/UI.");
        }
        await SelectTheme("system");
        Theme.Apply("system", true);
        if (!Theme.IsDark) throw new InvalidOperationException("System dark event failed.");
        Theme.Apply("system", false);
        if (Theme.IsDark) throw new InvalidOperationException("System light event failed.");
        await SelectTheme("dark");
        LoadPreferences();
        if (Theme.Mode != "dark") throw new InvalidOperationException("Theme preference not restored.");
        await SelectTheme("light");
        _content.Content = _pages[5];

        var platform = AdapterChoice("wecom", "企业微信");
        var agent = AdapterChoice("codex", "Codex CLI");
        if (platform.Items.Count != 1 || Selected(platform) != "wecom" || agent.Items.Count != 1 || Selected(agent) != "codex")
            throw new InvalidOperationException("Adapter dropdown selection failed.");

        ShowInTaskbar = false; ShowActivated = false;
        WindowStartupLocation = WindowStartupLocation.Manual; Left = -10000; Top = -10000;
        Show();
        await Dispatcher.Yield(DispatcherPriority.ContextIdle);
        await HandleWindowCloseAsync();
        if (IsVisible || _tray == null || _closing)
            throw new InvalidOperationException("Online window close did not hide to a real shell tray icon.");
        OpenFromTray();
        if (!IsVisible) throw new InvalidOperationException("Tray restore failed.");
        Hide();
    }
    internal async Task VerifyOfflineCloseAsync()
    {
        var offline = new MainWindow(Client) { IsVerification = true };
        bool closed = false;
        offline.Closed += (_, _) => closed = true;
        await offline.HandleWindowCloseAsync();
        if (!closed || !offline._closing || offline._tray != null)
            throw new InvalidOperationException("Offline close left an application/tray instance.");
    }
}
