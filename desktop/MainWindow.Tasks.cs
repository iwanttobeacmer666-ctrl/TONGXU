using System.Collections.ObjectModel;
using System.Text;
using System.Text.Json.Nodes;
using System.Windows;
using System.Windows.Controls;
using System.Windows.Data;

namespace Tongxu.Desktop;

public partial class MainWindow
{
    readonly ObservableCollection<ViewRow> _tasks = [];
    ComboBox _taskGroup = null!, _taskState = null!; TextBox _taskUser = null!, _eventSearch = null!, _eventOutput = null!; DataGrid _taskTable = null!; TextBlock _taskInfo = null!;
    JsonNode? _taskDetail;
    FrameworkElement BuildTasks()
    {
        var page = Page("任务审查", "查看全部群与用户的真实状态和公开执行记录；不展示模型内部思考。");
        var g = G("Auto", "*", "6", "*", "Auto");
        Put(page, g, 1);
        var filters = Flow();
        _taskGroup = new ComboBox();
        _taskGroup.SelectionChanged += (_, _) => FilterTasks();
        _taskState = new ComboBox();
        Choices(_taskState, new[] { new Choice("", Localization.T("全部状态")) }.Concat(new[] { "downloading", "queued", "running", "waiting_approval", "publishing", "stopping", "cleanup_failed", "stopped", "completed", "failed" }.Select(s => new Choice(s, J.State(s)))));
        _taskState.SelectionChanged += (_, _) => FilterTasks();
        _taskUser = Input("按用户 ID 搜索");
        _taskUser.TextChanged += (_, _) => FilterTasks();
        filters.Children.Add(Field("群", _taskGroup, 220));
        filters.Children.Add(Field("用户", _taskUser, 220));
        filters.Children.Add(Field("状态", _taskState, 180));
        Put(g, filters, 0);
        _taskTable = Table(("所属群", "TaskGroup", -1), ("用户", "TaskUser", -1), ("状态", "Status", 100), ("创建时间", "Created", 135), ("任务编号", "ID", 115));
        _taskTable.ItemsSource = _tasks;
        _taskTable.SelectionChanged += async (_, _) => { if (_ready) await RefreshAsync(false); };
        Put(g, _taskTable, 1);
        Put(g, new GridSplitter { Height = 6, HorizontalAlignment = HorizontalAlignment.Stretch, VerticalAlignment = VerticalAlignment.Center, Background = Brush("#DFE6E1"), ResizeDirection = GridResizeDirection.Rows }, 2);
        var detail = G("Auto", "*");
        detail.Margin = new Thickness(0, 16, 0, 0);
        var bar = Flow();
        _taskInfo = T("选择任务查看详情", 12, false, true);
        bar.Children.Add(_taskInfo);
        _eventSearch = Input("搜索公开事件", 180);
        _eventSearch.TextChanged += (_, _) => ShowEvents();
        bar.Children.Add(_eventSearch);
        bar.Children.Add(B("复制可见输出", () => { Clipboard.SetText(_eventOutput.Text); Notice(Localization.T("已复制当前可见的脱敏输出。")); return Task.CompletedTask; }));
        bar.Children.Add(B("停止任务", StopSelectedTaskAsync, "Danger"));
        Put(detail, bar, 0);
        _eventOutput = new TextBox { IsReadOnly = true, AcceptsReturn = true, TextWrapping = TextWrapping.NoWrap, HorizontalScrollBarVisibility = ScrollBarVisibility.Auto, VerticalScrollBarVisibility = ScrollBarVisibility.Auto, FontFamily = new System.Windows.Media.FontFamily("Cascadia Mono, Consolas, Microsoft YaHei UI"), FontSize = 12 };
        Put(detail, _eventOutput, 1);
        Put(g, detail, 3);
        Put(g, T("仅保留内存中最近 50 个已结束任务；每个任务最多 40 条事件。重启后记录不保留，进度不估算百分比。", 11, false, true), 4);
        return page;
    }
    void FilterTasks()
    {
        if (_taskTable == null)
            return;
        CollectionViewSource.GetDefaultView(_tasks).Filter = o => o is ViewRow r && (Selected(_taskGroup) == "" || r.TaskGroup == Selected(_taskGroup)) && (Selected(_taskState) == "" || r.State == Selected(_taskState)) && r.TaskUser.Contains(_taskUser.Text, StringComparison.OrdinalIgnoreCase);
    }
    async Task RefreshTasksAsync()
    {
        var reply = await Client.SendAsync(new()
        {
            ["Action"] = "tasks"
        });
        Check(reply);
        Sync(_tasks, reply.Rows("TaskItems"), n => n.S("ID"));
        FilterTasks();
        if (_taskTable.SelectedItem == null)
            _taskTable.SelectedItem = _taskTable.Items.OfType<ViewRow>().FirstOrDefault();
        if (_taskTable.SelectedItem is ViewRow selected)
        {
            var id = selected.ID;
            var detail = await Client.SendAsync(new()
            {
                ["Action"] = "task-detail",
                ["Task"] = id
            });
            if ((_taskTable.SelectedItem as ViewRow)?.ID != id)
                return;
            Check(detail);
            _taskDetail = detail.Rows("TaskItems").FirstOrDefault()?.DeepClone();
            UpdateTaskInfo();
            ShowEvents();
        }
        else
        {
            _taskDetail = null;
            _taskInfo.Text = _tasks.Count == 0 ? Localization.T("当前没有可保留的任务记录。") : Localization.T("选择任务查看详情");
            ShowEvents();
        }
    }
    void UpdateTaskInfo()
    {
        if (!_online)
            _taskInfo.Text = _serviceKnown ? Localization.T("服务离线 · 下方为上次读取的记录，当前状态需在线核对。") : Localization.T("服务状态未确认 · 下方为上次读取的记录。");
        else if (_taskDetail != null)
            _taskInfo.Text = Localization.F($"{J.State(_taskDetail.S("State"))} · 创建 {J.Time(_taskDetail.S("Created"))} · 开始 {J.Time(_taskDetail.S("Started"))} · 结束 {J.Time(_taskDetail.S("Ended"))}");
        else
            _taskInfo.Text = _tasks.Count == 0 ? Localization.T("当前没有可保留的任务记录。") : Localization.T("选择任务查看详情");
    }
    void ShowEvents()
    {
        if (_eventOutput == null)
            return;
        var text = new StringBuilder();
        foreach (var item in _taskDetail.Rows("Events").OrderBy(n => n.N("Sequence")))
        {
            var value = SafeText.Clean(item.S("Text"));
            if (!(value + " " + item.S("Kind")).Contains(_eventSearch.Text, StringComparison.OrdinalIgnoreCase))
                continue;
            text.AppendLine($"#{item.N("Sequence")}  {J.Time(item.S("At"))}  {item.S("Kind")}");
            if (value != "")
                text.AppendLine(value);
            text.AppendLine();
        }
        var next = text.ToString();
        if (next == _eventOutput.Text)
            return;
        var offset = _eventOutput.VerticalOffset;
        var end = offset >= _eventOutput.ExtentHeight - _eventOutput.ViewportHeight - 3;
        var caret = _eventOutput.CaretIndex;
        _eventOutput.Text = next;
        _eventOutput.CaretIndex = Math.Min(caret, next.Length);
        if (end)
            _eventOutput.ScrollToEnd();
        else
            _eventOutput.ScrollToVerticalOffset(offset);
    }
    async Task StopSelectedTaskAsync()
    {
        if (_taskTable.SelectedItem is not ViewRow row)
            throw new InvalidOperationException(Localization.T("请选择任务。"));
        if (!row.Data.B("CanStop"))
            throw new InvalidOperationException(Localization.T("该任务当前不可停止；可能正在发布或已结束，请刷新核对。"));
        Notice(Localization.T("正在请求停止，等待后端确认任务与临时清理结果…"));
        var reply = await Client.SendAsync(new()
        {
            ["Action"] = "stop",
            ["Task"] = row.ID
        });
        await RefreshTasksAsync();
        Check(reply);
        Notice(reply.S("Tasks") is { Length: > 0 } t ? t : Localization.T("后端已确认停止及清理完成。"));
    }
    void UpdateGroupChoices()
    {
        var groups = Groups.Select(g => new Choice(g.S("ID"), g.S("ID"))).ToList();
        Choices(_taskGroup, new[] { new Choice("", Localization.T("全部群")) }.Concat(groups));
        _docChanging = true;
        Choices(_docGroup, groups);
        _docChanging = false;
        UpdateDocumentScopes();
    }
}
