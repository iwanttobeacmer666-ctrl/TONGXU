using System.Collections.ObjectModel;
using System.ComponentModel;
using System.IO;
using System.Text.Json.Nodes;
using System.Windows;
using System.Windows.Controls;
using System.Windows.Data;
using System.Windows.Media;
using System.Windows.Media.Animation;
using System.Windows.Threading;

namespace Tongxu.Desktop;

public partial class MainWindow : Window
{
    internal readonly LocalClient Client;
    readonly SemaphoreSlim _work = new(1, 1);
    readonly DispatcherTimer _poll = new() { Interval = TimeSpan.FromSeconds(3) };
    readonly ContentControl _content = new(); readonly TextBlock _notice = new() { Text = Localization.T("正在读取本地状态…") };
    readonly ProgressBar _progress = new() { IsIndeterminate = true, Width = 90, Height = 3, Visibility = Visibility.Collapsed, Margin = new Thickness(12, 0, 0, 0) };
    readonly ListBox _nav = new() { BorderThickness = new Thickness(0), Background = Brushes.Transparent, ItemContainerStyle = (Style)Application.Current.FindResource("Nav") };
    readonly List<FrameworkElement> _pages = [];
    readonly TextBlock _brandLabel;
    internal readonly Grid Surface = new();
    JsonNode? _roster, _settings, _status; string _version = "", _owner = ""; bool _online, _serviceKnown, _ready, _closing;
    Task<JsonNode>? _launcher; bool _commandPending;
    internal bool IsVerification
    {
        get; set;
    }
    static readonly string[] Titles = ["连接与运行", "群与成员", "已停用用户", "任务审查", "文档管理", "设置"];
    public MainWindow(LocalClient client)
    {
        Client = client;
        Title = Localization.T("同序 · 工作台");
        var area = SystemParameters.WorkArea;
        Width = Math.Min(1360, area.Width);
        Height = Math.Min(900, area.Height);
        MinWidth = Math.Min(960, area.Width);
        MinHeight = Math.Min(640, area.Height);
        WindowStartupLocation = WindowStartupLocation.CenterScreen;
        Localization.Bind(this, TitleProperty, "同序 · 工作台");
        var body = new Grid();
        body.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(224) });
        body.ColumnDefinitions.Add(new ColumnDefinition());
        WindowCaption.Attach(this, body, Surface, 224);
        Surface.SizeChanged += (_, e) => body.ColumnDefinitions[0].Width = new GridLength(e.NewSize.Width < 1100 ? 190 : 224);
        var side = new Grid { Background = Brush("#F0F3F1"), Margin = new Thickness(0) };
        side.RowDefinitions.Add(new RowDefinition { Height = GridLength.Auto });
        side.RowDefinitions.Add(new RowDefinition());
        side.RowDefinitions.Add(new RowDefinition { Height = GridLength.Auto });
        body.Children.Add(side);
        var brand = V();
        brand.Margin = new Thickness(22, 30, 16, 28);
        _brandLabel = T("同序", 24, true);
        var identity = H();
        identity.Children.Add(new Image { Source = Icon, Width = 34, Height = 34, Margin = new Thickness(0, 0, 10, 8) });
        identity.Children.Add(_brandLabel);
        brand.Children.Add(identity);
        brand.Children.Add(T("工作台", 11, false, true));
        side.Children.Add(brand);
        _nav.Margin = new Thickness(16, 0, 16, 0);
        ScrollViewer.SetHorizontalScrollBarVisibility(_nav, ScrollBarVisibility.Disabled);
        foreach (var title in Titles)
        {
            var label = new TextBlock { TextWrapping = TextWrapping.Wrap };
            label.SetResourceReference(TextBlock.ForegroundProperty, "Ink");
            Localization.Bind(label, TextBlock.TextProperty, title);
            _nav.Items.Add(label);
        }
        Put(side, _nav, 1);
        var admin = V();
        admin.Margin = new Thickness(22, 20, 16, 24);
        admin.Children.Add(T("本机机主", 13, true));
        admin.Children.Add(T("唯一全局管理员", 11, false, true));
        Put(side, admin, 2);
        var work = new Grid { Background = Theme.Get("Overlay") };
        work.RowDefinitions.Add(new RowDefinition());
        work.RowDefinitions.Add(new RowDefinition { Height = GridLength.Auto });
        Grid.SetColumn(work, 1);
        body.Children.Add(work);
        _content.Margin = new Thickness(30, 26, 30, 8);
        work.Children.Add(_content);
        var footer = new Grid { Margin = new Thickness(30, 10, 24, 14) };
        footer.ColumnDefinitions.Add(new());
        footer.ColumnDefinitions.Add(new()
        {
            Width = GridLength.Auto
        });
        _notice.MaxHeight = 72;
        _notice.SetBinding(ToolTipProperty, new Binding("Text") { Source = _notice });
        footer.Children.Add(_notice);
        Grid.SetColumn(_progress, 1);
        footer.Children.Add(_progress);
        Put(work, footer, 1);
        _pages.Add(BuildConnection());
        _pages.Add(BuildMembers());
        _pages.Add(BuildDisabled());
        _pages.Add(BuildTasks());
        _pages.Add(BuildDocuments());
        _pages.Add(BuildSettings());
        _nav.SelectionChanged += async (_, _) => { if (_nav.SelectedIndex < 0) return; _content.Content = _pages[_nav.SelectedIndex]; if (_ready) { Fade(); await RefreshAsync(true); } };
        _nav.SelectedIndex = 0;
        _poll.Tick += async (_, _) => { if (_ready && _work.CurrentCount > 0) await RefreshAsync(false); };
        Loaded += async (_, _) => { if (!IsVerification) { await InitializeAsync(); _poll.Start(); } };
        Closing += OnClosing;
        Closed += (_, _) => { _poll.Stop(); _tray?.Dispose(); Microsoft.Win32.SystemEvents.UserPreferenceChanged -= OnSystemThemeChanged; };
        LoadPreferences();
        Microsoft.Win32.SystemEvents.UserPreferenceChanged += OnSystemThemeChanged;
    }
    internal async Task InitializeAsync()
    {
        await RefreshAsync(true);
        _ready = true;
    }
    internal async Task NavigateAsync(int index)
    {
        _nav.SelectedIndex = index;
        await RefreshAsync(true);
    }
    void Fade()
    {
        if (SystemParameters.ClientAreaAnimation && !IsVerification)
            _content.BeginAnimation(OpacityProperty, new DoubleAnimation(.75, 1, TimeSpan.FromMilliseconds(120)));
    }
    void OnClosing(object? sender, CancelEventArgs e)
    {
        if (IsVerification || _closing)
            return;
        e.Cancel = true;
        _ = HandleWindowCloseAsync();
    }
    internal async Task RefreshAsync(bool force)
    {
        if (!force && !await _work.WaitAsync(0))
            return;
        if (force)
            await _work.WaitAsync();
        try
        {
            await RefreshCoreAsync(force);
        }
        catch (Exception ex) { _serviceKnown = false; UpdateConnection(); if (_status.S("Service") == "stopping") Notice(Localization.T("正在等待服务完全退出，暂不重复提交停止请求。清理结果尚未单独确认。")); else Notice(Localization.T("当前服务状态未确认。") + SafeText.Clean(ex.Message), true); }
        finally { _work.Release(); }
    }
    async Task RefreshCoreAsync(bool force)
    {
        var wasOnline = _online;
        var wasKnown = _serviceKnown;
        var status = await Client.SendAsync(new()
        {
            ["Action"] = "status"
        });
        _serviceKnown = status.B("Success") && status["Status"] != null;
        _online = status.B("Online");
        _status = status["Status"]?.DeepClone();
        var roster = await Client.SendAsync(new()
        {
            ["Action"] = "list"
        });
        if (roster.B("Success"))
        {
            bool changed = _roster == null || _version != roster.S("Version");
            _roster = roster;
            _version = roster.S("Version");
            _owner = roster.S("Owner");
            if (changed)
                UpdateRosterViews();
        }
        if (_settings == null || force && _nav.SelectedIndex is 0 or 5)
        {
            var settings = await Client.SettingsAsync("read");
            if (settings.B("Success"))
            {
                _settings = settings;
                _owner = settings.S("Owner");
                UpdateSettingsView();
            }
        }
        UpdateConnection();
        if (_nav.SelectedIndex == 3 && _online)
            await RefreshTasksAsync();
        if (_nav.SelectedIndex == 3 && !_online)
            _taskInfo.Text = _serviceKnown ? Localization.T("服务离线 · 下方为上次读取的记录，当前状态需在线核对。") : Localization.T("服务状态未确认 · 下方为上次读取的记录。");
        if (_nav.SelectedIndex == 4 && (force || !_online))
            await LoadDocumentsAsync();
        if (!_serviceKnown)
            Notice(status.S("Message") is { Length: > 0 } message ? message : Localization.T("服务状态未确认，请刷新核对。"), true);
        else if (!roster.B("Success") && force)
            Notice(roster.S("Message"), true);
        else if (!_ready || !wasKnown || wasOnline != _online)
            Notice(_online ? Localization.T("已连接本地服务。") : Localization.T("服务未运行；群与成员可离线保存，下次启动生效。"));
    }
    async Task RunAsync(Func<Task> action, string? busy = null)
    {
        if (_commandPending || _exitPending || _closePending)
            return;
        _commandPending = true;
        _content.IsEnabled = false;
        _progress.Visibility = Visibility.Visible;
        Notice(busy ?? Localization.T("处理中，请等待后端确认…"));
        await _work.WaitAsync();
        try
        {
            await action();
        }
        catch (Exception ex) { Notice(SafeText.Clean(ex.Message), true); }
        finally { _content.IsEnabled = true; _progress.Visibility = Visibility.Collapsed; _work.Release(); _commandPending = false; }
    }
    internal void Notice(string text, bool error = false)
    {
        _notice.Text = Localization.T(text);
        _notice.Foreground = Brush(error ? "#AA3A3A" : "#6B7771");
    }
    static void Check(JsonNode r)
    {
        if (!r.B("Success"))
            throw new InvalidOperationException(r.S("Message") is { Length: > 0 } m ? m : Localization.T("操作未完成，请刷新核对。"));
    }
    bool Confirm(string title, string body) => IsVerification || MessageBox.Show(this, body, title, MessageBoxButton.OKCancel, MessageBoxImage.Warning) == MessageBoxResult.OK;
    static SolidColorBrush Brush(string color) => Theme.Get(color switch
    {
        "#F0F3F1" => "Sidebar", "#AA3A3A" => "Error", "#6B7771" => "Muted",
        "#202A26" => "Ink", "#DFE6E1" => "Line", _ => throw new ArgumentException("Unknown interface color.")
    });
    static TextBlock T(string value, int size = 13, bool bold = false, bool muted = false)
    {
        var block = new TextBlock { FontSize = size, FontWeight = bold ? FontWeights.SemiBold : FontWeights.Normal, Foreground = Brush(muted ? "#6B7771" : "#202A26"), Margin = new Thickness(0, 0, 0, 8) };
        Localization.Bind(block, TextBlock.TextProperty, value);
        return block;
    }
    static StackPanel V() => new() { Orientation = Orientation.Vertical }; static StackPanel H() => new() { Orientation = Orientation.Horizontal, VerticalAlignment = VerticalAlignment.Center };
    static WrapPanel Flow() => new() { Margin = new Thickness(0, 0, 0, 16) };
    Button B(string label, Func<Task> click, string? style = null)
    {
        var b = new Button { Content = label };
        Localization.Bind(b, ContentControl.ContentProperty, label);
        if (style != null)
            b.Style = (Style)FindResource(style);
        b.Click += async (_, _) => await RunAsync(click);
        return b;
    }
    static Grid G(params string[] sizes)
    {
        var g = new Grid();
        foreach (var s in sizes)
            g.RowDefinitions.Add(new RowDefinition { Height = s == "*" ? new GridLength(1, GridUnitType.Star) : s == "Auto" ? GridLength.Auto : new GridLength(double.Parse(s)) });
        return g;
    }
    static void Put(Grid g, UIElement e, int row)
    {
        Grid.SetRow(e, row);
        g.Children.Add(e);
    }
    Grid Page(string title, string description)
    {
        var g = G("Auto", "*");
        var h = V();
        h.Children.Add(T(title, 28, true));
        h.Children.Add(T(description, 12, false, true));
        h.Margin = new Thickness(0, 0, 0, 16);
        Put(g, h, 0);
        return g;
    }
    static FrameworkElement Scroll(UIElement element) => new ScrollViewer { Content = element, VerticalScrollBarVisibility = ScrollBarVisibility.Auto, HorizontalScrollBarVisibility = ScrollBarVisibility.Disabled };
    static FrameworkElement Field(string title, FrameworkElement control, double width = 240)
    {
        var s = V();
        s.Width = width;
        s.HorizontalAlignment = HorizontalAlignment.Left;
        s.Margin = new Thickness(0, 0, 10, 14);
        s.Children.Add(T(title, 12, false, true));
        s.Children.Add(control);
        return s;
    }
    static TextBox Input(string hint = "", double width = double.NaN)
    {
        var box = new TextBox { ToolTip = hint, Tag = hint, Width = width, Style = (Style)Application.Current.FindResource("SearchInput") };
        Localization.Bind(box, ToolTipProperty, hint);
        Localization.Bind(box, TagProperty, hint);
        Localization.Bind(box, System.Windows.Automation.AutomationProperties.NameProperty, hint);
        return box;
    }
    DataGrid Table(params (string name, string path, double width)[] columns)
    {
        var d = new DataGrid();
        foreach (var (name, path, width) in columns)
        {
            var style = (Style)FindResource("CellText");
            if (path == "Status")
            {
                style = new Style(typeof(TextBlock), style);
                style.Setters.Add(new Setter(TextBlock.ForegroundProperty, new Binding("StatusColor")));
            }
            var column = new DataGridTextColumn { Binding = new Binding(path), Width = width < 0 ? new DataGridLength(-width, DataGridLengthUnitType.Star) : new DataGridLength(width), ElementStyle = style };
            Localization.Bind(column, DataGridColumn.HeaderProperty, name);
            d.Columns.Add(column);
        }
        return d;
    }
    static void Sync(ObservableCollection<ViewRow> rows, IEnumerable<JsonNode> values, Func<JsonNode, string> key)
    {
        var nodes = values.ToList();
        var keys = nodes.Select(key).ToHashSet(StringComparer.Ordinal);
        for (int i = rows.Count - 1; i >= 0; i--)
            if (!keys.Contains(rows[i].Key))
                rows.RemoveAt(i);
        var existing = rows.ToDictionary(r => r.Key, StringComparer.Ordinal);
        foreach (var n in nodes)
        {
            var k = key(n);
            if (!existing.TryGetValue(k, out var old))
                rows.Add(new ViewRow { Key = k, Data = n });
            else if (old.Data?.ToJsonString() != n.ToJsonString())
                old.Data = n;
        }
    }
    static void Choices(ComboBox box, IEnumerable<Choice> values, bool first = true)
    {
        var list = values.ToList();
        var selected = (box.SelectedItem as Choice)?.ID;
        var previous = (box.ItemsSource as IEnumerable<Choice>)?.ToList();
        if (previous != null && previous.SequenceEqual(list))
            return;
        box.ItemsSource = list;
        box.SelectedItem = list.FirstOrDefault(c => c.ID == selected) ?? (first ? list.FirstOrDefault() : null);
    }
    static string Selected(ComboBox box) => (box.SelectedItem as Choice)?.ID ?? "";
    IEnumerable<JsonNode> Groups => _roster.Rows("Groups");
    async Task MutationAsync(JsonObject request)
    {
        request["Version"] = _version;
        var reply = await Client.SendAsync(request);
        await RefreshCoreAsync(false);
        if (!reply.B("Success"))
        {
            Notice(reply.S("Message") + (reply.B("Applied") ? Localization.T(" 设置已保存，但清理尚未确认。") : ""), true);
            return;
        }
        Notice(reply.S("Message") is { Length: > 0 } m ? m : !reply.B("Online") ? Localization.T("已保存，下次启动服务生效。") : reply.B("CleanupReady") ? Localization.T("后端已确认操作完成。") : Localization.T("请求已处理，清理状态尚未确认。"));
    }
}
