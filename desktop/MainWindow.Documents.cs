using System.Collections.ObjectModel;
using System.Text.Json.Nodes;
using System.Windows;
using System.Windows.Controls;
using System.Windows.Data;

namespace Tongxu.Desktop;

public partial class MainWindow
{
    readonly ObservableCollection<ViewRow> _documents = []; readonly Stack<int> _docPrevious = [];
    ComboBox _docGroup = null!, _docScope = null!; DataGrid _docTable = null!; TextBlock _docLabel = null!, _docPageLabel = null!; TextBox _docSearch = null!;
    int _docOffset, _docNext, _docTotal; bool _docChanging, _temporaryBusy; string _loadedScope = "";
    bool Temporary => Selected(_docScope) == "temporary";
    string DocumentUser => Selected(_docScope).StartsWith("private:") ? Selected(_docScope)[8..] : "public";
    FrameworkElement BuildDocuments()
    {
        var page = Page("文档管理", "管理员可读取和删除公共、私人文件；临时区只提供清单与清空。");
        var g = G("Auto", "Auto", "*", "Auto", "Auto");
        Put(page, g, 1);
        _docGroup = new ComboBox();
        _docScope = new ComboBox();
        _docGroup.SelectionChanged += async (_, _) => { if (_docChanging) return; UpdateDocumentScopes(); await DocumentScopeChangedAsync(); };
        _docScope.SelectionChanged += async (_, _) => await DocumentScopeChangedAsync();
        var scopes = Flow();
        scopes.Children.Add(Field("群", _docGroup, 320));
        scopes.Children.Add(Field("文件区", _docScope, 360));
        Put(g, scopes, 0);
        var actions = Flow();
        _docSearch = Input("仅筛选当前页文件名", 240);
        _docSearch.TextChanged += (_, _) => { CollectionViewSource.GetDefaultView(_documents).Filter = o => o is ViewRow r && r.Name.Contains(_docSearch.Text, StringComparison.OrdinalIgnoreCase); };
        actions.Children.Add(_docSearch);
        actions.Children.Add(B("读取文件", ReadDocumentAsync));
        actions.Children.Add(B("删除所选文件", () => DeleteDocumentsAsync(false), "Danger"));
        actions.Children.Add(B("清空当前文件区", () => DeleteDocumentsAsync(true), "Danger"));
        Put(g, actions, 1);
        _docTable = Table(("名称（Ctrl / Shift 多选）", "Name", -3), ("类型", "FileStatus", 100), ("大小", "Size", 100));
        _docTable.SelectionMode = DataGridSelectionMode.Extended;
        _docTable.ItemsSource = _documents;
        Put(g, _docTable, 2);
        var pager = Flow();
        pager.Margin = new Thickness(0, 16, 0, 12);
        _docPageLabel = T("尚未加载", 12, false, true);
        pager.Children.Add(_docPageLabel);
        pager.Children.Add(B("上一页", async () => { if (_docPrevious.Count == 0) return; _docOffset = _docPrevious.Pop(); await LoadDocumentsAsync(); }));
        pager.Children.Add(B("下一页", async () => { if (_docNext >= _docTotal) return; _docPrevious.Push(_docOffset); _docOffset = _docNext; await LoadDocumentsAsync(); }));
        pager.Children.Add(B("刷新列表", () => LoadDocumentsAsync()));
        Put(g, pager, 3);
        _docLabel = T("请选择群与文件区。", 12, false, true);
        Put(g, _docLabel, 4);
        return page;
    }
    void UpdateDocumentScopes()
    {
        if (_docScope == null)
            return;
        _docChanging = true;
        var choices = new List<Choice> { new("public", Localization.T("公共文件")), new("private:" + _owner, Localization.T("私人 · 管理员 ") + _owner) };
        choices.AddRange(Groups.FirstOrDefault(g => g.S("ID") == Selected(_docGroup)).Rows("Members").Select(u => new Choice("private:" + u.S("ID"), Localization.T("私人 · ") + u.S("ID"))));
        choices.Add(new("temporary", Localization.T("全局临时区")));
        Choices(_docScope, choices);
        _docChanging = false;
        _docGroup.IsEnabled = !Temporary;
    }
    async Task DocumentScopeChangedAsync()
    {
        if (_docChanging || !_ready)
            return;
        var key = Selected(_docGroup) + "\0" + Selected(_docScope);
        if (key == _loadedScope)
            return;
        _docPrevious.Clear();
        _docOffset = 0;
        await RefreshAsync(true);
    }
    JsonObject DocRequest(string action) => Temporary ? new() { ["Action"] = action } : new() { ["Action"] = action, ["Group"] = Selected(_docGroup), ["User"] = DocumentUser };
    async Task LoadDocumentsAsync()
    {
        if (!_online || !_serviceKnown)
        {
            _documents.Clear();
            _docPageLabel.Text = _serviceKnown ? Localization.T("服务离线") : Localization.T("服务状态未确认");
            _docLabel.Text = Localization.T("文件列表、读取和删除必须在本地服务在线时操作。");
            return;
        }
        if (!Temporary && Selected(_docGroup) == "")
            return;
        var key = Selected(_docGroup) + "\0" + Selected(_docScope);
        if (key != _loadedScope)
        {
            _docOffset = 0;
            _docPrevious.Clear();
            _loadedScope = key;
        }
        var request = DocRequest(Temporary ? "temporary-files" : "documents");
        request["Offset"] = _docOffset;
        request["Limit"] = 50;
        var reply = await Client.SendAsync(request);
        Check(reply);
        var data = reply[Temporary ? "FileOperation" : "Documents"];
        _docTotal = (int)data.N("Total");
        _docNext = (int)data.N("NextOffset");
        _temporaryBusy = data.B("Busy");
        Sync(_documents, data.Rows(Temporary ? "Items" : "Files"), n => n.S("Name"));
        UpdateDocumentLabels();
    }
    void UpdateDocumentLabels()
    {
        if (!_online || !_serviceKnown)
        {
            _docPageLabel.Text = _serviceKnown ? Localization.T("服务离线") : Localization.T("服务状态未确认");
            _docLabel.Text = Localization.T("文件列表、读取和删除必须在本地服务在线时操作。");
            return;
        }
        _docPageLabel.Text = Localization.F($"共 {_docTotal} 个{(Temporary ? Localization.T("文件/目录") : Localization.T("文件"))} · 当前 {_docOffset + (_docTotal > 0 ? 1 : 0)}–{_docNext}");
        _docLabel.Text = Temporary ? (_temporaryBusy ? Localization.T("临时区正被任务或内部操作使用，禁止清空。不会自动停止任务。") : Localization.T("全局临时区：只允许预览完整清单并清空；不能读取文件，也不能猜测群或用户归属。")) : Localization.F($"当前群：{Selected(_docGroup)}   范围：{(DocumentUser == "public" ? Localization.T("公共文件") : Localization.T("私人文件 · ") + DocumentUser)}\n清空包含全部子目录，不受当前分页或搜索影响；成员、依赖和会话保留。");
    }
    async Task ReadDocumentAsync()
    {
        if (Temporary)
            throw new InvalidOperationException(Localization.T("临时区只提供清单和清空，不提供内容读取。"));
        if (!_online)
            throw new InvalidOperationException(Localization.T("请先启动服务。"));
        if (_docTable.SelectedItem is not ViewRow row)
            throw new InvalidOperationException(Localization.T("请选择文件。"));
        var viewer = new DocumentWindow(Client, Selected(_docGroup), DocumentUser, row.Name) { Owner = this };
        viewer.ShowDialog();
        await Task.CompletedTask;
    }
    async Task DeleteDocumentsAsync(bool clear)
    {
        if (!_online)
            throw new InvalidOperationException(Localization.T("文件操作需要本地服务在线。"));
        if (Temporary && !clear)
            throw new InvalidOperationException(Localization.T("临时区只允许完整预览后清空，不提供逐项删除。"));
        var action = Temporary ? "temporary-clear-preview" : clear ? "documents-clear-preview" : "documents-delete-preview";
        var request = DocRequest(action);
        request["Version"] = _version;
        request["Limit"] = 100;
        if (!clear)
        {
            var selected = _docTable.SelectedItems.Cast<ViewRow>().ToList();
            if (selected.Count is < 1 or > 100)
                throw new InvalidOperationException(Localization.T("每批请选择 1–100 个文件，更多文件请分批操作。"));
            request["Names"] = new JsonArray(selected.Select(r => JsonValue.Create(r.Name)).ToArray());
        }
        var reply = await Client.SendAsync(request);
        Check(reply);
        var initial = reply["FileOperation"]?.DeepClone() ?? throw new InvalidOperationException(Localization.T("后端未返回完整预览。"));
        var review = new FileReviewWindow(Client, initial, clear ? Localization.T("清空当前文件区") : Localization.T("删除所选文件")) { Owner = this };
        if (review.ShowDialog() != true)
            return;
        request.Remove("Names");
        request.Remove("Limit");
        request["Action"] = Temporary ? "temporary-clear" : clear ? "documents-clear" : "documents-delete";
        request["Confirm"] = initial.S("Token");
        var result = await Client.SendAsync(request);
        string refreshWarning = "";
        try
        {
            await LoadDocumentsAsync();
        }
        catch { refreshWarning = Localization.T(" 列表刷新失败，请手动刷新核对。"); }
        if (!result.B("Success") || !result["FileOperation"].B("Complete"))
        {
            var state = result["FileOperation"];
            if (state != null)
                new FileReviewWindow(Client, state.DeepClone(), Localization.T("操作未全部完成"), true) { Owner = this }.ShowDialog();
            throw new InvalidOperationException(result.S("Message") + Localization.T(" 已删除 ") + state.N("Deleted") + Localization.T(" 项，失败 ") + state.N("Failed") + Localization.T(" 项；请核对剩余数据后重新预览。"));
        }
        Notice(Localization.T("后端已确认清理完成；其他文件区、成员及会话保留。") + refreshWarning, refreshWarning != "");
    }
}
