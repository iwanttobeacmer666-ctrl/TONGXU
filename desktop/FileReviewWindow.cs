using System.Collections.ObjectModel;
using System.Text.Json.Nodes;
using System.Windows;
using System.Windows.Controls;
using System.Windows.Data;

namespace Tongxu.Desktop;

public sealed class FileReviewWindow : Window
{
    readonly LocalClient _client; readonly JsonNode _initial; readonly bool _results;
    readonly ObservableCollection<ManifestRow> _rows = []; readonly TextBlock _status = new() { Text = Localization.T("正在加载完整后端清单…"), TextWrapping = TextWrapping.Wrap }; readonly Button _confirm;
    bool _closed; Task? _loadTask;
    internal bool ReadyToConfirm => _confirm.IsEnabled;
    public FileReviewWindow(LocalClient client, JsonNode initial, string title, bool results = false)
    {
        _client = client;
        _initial = initial;
        _results = results;
        Title = title;
        Width = 880;
        Height = 660;
        MinWidth = 680;
        MinHeight = 480;
        WindowStartupLocation = WindowStartupLocation.CenterOwner;
        var grid = new Grid { Margin = new Thickness(28) };
        foreach (var h in new[] { GridLength.Auto, GridLength.Auto, new GridLength(1, GridUnitType.Star), GridLength.Auto, GridLength.Auto })
            grid.RowDefinitions.Add(new()
            {
                Height = h
            });
        Content = grid;
        var heading = new TextBlock { Text = title, FontSize = 24, FontWeight = FontWeights.SemiBold, Margin = new Thickness(0, 0, 0, 14) };
        grid.Children.Add(heading);
        var scope = _initial.S("Kind") == "temporary" ? Localization.T("全局任务临时区（所有群共用）") : Localization.F($"群：{initial.S("Group")}\n范围：{(initial.S("User") == "public" ? Localization.T("公共文件") : Localization.T("私人文件 · ") + initial.S("User"))}");
        var summary = new TextBlock { Text = Localization.F($"{scope}\n清单：{initial.N("Files")} 个文件、{initial.N("Directories")} 个目录，共 {J.Bytes(initial.N("Bytes"))}\n{(results ? Localization.T("以下为实际逐项结果，成功删除不会回滚。") : Localization.T("成员、依赖和会话保留。请核对完整范围；此删除不可撤销。"))}"), Margin = new Thickness(0, 0, 0, 18) };
        Grid.SetRow(summary, 1);
        grid.Children.Add(summary);
        var table = new DataGrid { ItemsSource = _rows };
        foreach (var (label, path, width) in new[] { (Localization.T("相对名称"), "Name", -1d), (Localization.T("类型"), "Type", 75d), (Localization.T("大小"), "Bytes", 90d), (Localization.T("结果"), "Outcome", 160d) })
            table.Columns.Add(new DataGridTextColumn { Header = label, Binding = new Binding(path), Width = width < 0 ? new DataGridLength(1, DataGridLengthUnitType.Star) : new DataGridLength(width), ElementStyle = (Style)Application.Current.FindResource("CellText") });
        Grid.SetRow(table, 2);
        grid.Children.Add(table);
        _status.Margin = new Thickness(0, 14, 0, 14);
        Grid.SetRow(_status, 3);
        grid.Children.Add(_status);
        var actions = new StackPanel { Orientation = Orientation.Horizontal, HorizontalAlignment = HorizontalAlignment.Right };
        var cancel = new Button { Content = results ? Localization.T("关闭") : Localization.T("取消"), IsCancel = true };
        _confirm = new Button { Content = Localization.T("确认删除清单中的全部项目"), IsEnabled = false, Style = (Style)Application.Current.FindResource("Danger") };
        _confirm.Click += (_, _) => { DialogResult = true; };
        actions.Children.Add(cancel);
        if (!results)
            actions.Children.Add(_confirm);
        Grid.SetRow(actions, 4);
        grid.Children.Add(actions);
        Loaded += async (_, _) => await LoadAsync();
        Closed += (_, _) => _closed = true;
        WindowCaption.Attach(this, grid);
    }
    internal Task LoadAsync() => _loadTask ??= LoadCoreAsync();
    async Task LoadCoreAsync()
    {
        try
        {
            var page = _initial;
            while (!_closed)
            {
                foreach (var n in page.Rows("Items"))
                    _rows.Add(new ManifestRow(n));
                _status.Text = Localization.F($"已加载 {_rows.Count} / {page.N("Total")} 项；确认有效至 {J.Time(_initial.S("Expires"))}。");
                if (page.N("NextOffset") >= page.N("Total"))
                    break;
                var request = new JsonObject { ["Action"] = "file-operation", ["Version"] = _initial.S("Version"), ["Confirm"] = _initial.S("Token"), ["Scope"] = _initial.S("Kind"), ["Offset"] = page.N("NextOffset"), ["Limit"] = 100 };
                if (_initial.S("Kind") != "temporary")
                {
                    request["Group"] = _initial.S("Group");
                    request["User"] = _initial.S("User");
                }
                var reply = await _client.SendAsync(request);
                if (!reply.B("Success"))
                    throw new InvalidOperationException(reply.S("Message"));
                page = reply["FileOperation"] ?? throw new InvalidOperationException(Localization.T("清单响应不完整。"));
            }
            if (_closed)
                return;
            _confirm.IsEnabled = !_results && page.B("ReviewComplete") && DateTimeOffset.TryParse(_initial.S("Expires"), out var expiry) && expiry > DateTimeOffset.UtcNow;
            if (_results)
                _status.Text = Localization.F($"实际删除 {_initial.N("Deleted")} 项，失败 {_initial.N("Failed")} 项，清空后剩余 {_initial.N("Remaining")} 项。失败后请刷新并重新预览，不要重复提交旧确认。");
        }
        catch (Exception ex) { if (!_closed) _status.Text = SafeText.Clean(ex.Message) + Localization.T(" 请关闭并重新预览。"); }
    }
    public record ManifestRow(JsonNode Node)
    {
        public string Name => Node.S("Name"); public string Type => Node.B("Blocked") ? Localization.T("需核查") : Node.B("Directory") ? Localization.T("目录") : Localization.T("文件"); public string Bytes => Node.B("Directory") ? "—" : J.Bytes(Node.N("Bytes"));
        public string Outcome => Node.S("Status") switch { "deleted" => Localization.T("已删除"), "failed" => Localization.T("失败 · 文件变化或被占用"), "not_attempted" => Localization.T("未执行 · 需重新核对"), _ => Localization.T("待确认") };
    }
}
