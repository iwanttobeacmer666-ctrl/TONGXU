using System.Collections.ObjectModel;
using System.Text.Json.Nodes;
using System.Windows;
using System.Windows.Controls;
using System.Windows.Data;

namespace Tongxu.Desktop;

public partial class MainWindow
{
    readonly ObservableCollection<ViewRow> _members = [], _disabled = [];
    ComboBox _memberGroup = null!, _disabledGroup = null!; TextBox _memberSearch = null!, _disabledSearch = null!; DataGrid _memberTable = null!, _disabledTable = null!; TextBlock _groupSummary = null!;
    bool _rosterChanging;
    FrameworkElement BuildMembers()
    {
        var page = Page("群与成员", "在线修改实时生效；停用会撤权并停止任务，资料与依赖保留。");
        var g = G("Auto", "Auto", "*", "Auto");
        Put(page, g, 1);
        _memberGroup = new ComboBox();
        _memberGroup.SelectionChanged += (_, _) => { if (!_rosterChanging) FilterMembers(); };
        var top = Flow();
        top.Children.Add(Field("当前群", _memberGroup, 350));
        top.Children.Add(B("添加群", async () => { var id = TextPrompt.Ask(this, Localization.T("添加群"), Localization.T("企业微信群稳定群 ID")); if (id != null) await MutationAsync(new() { ["Action"] = "group-add", ["Group"] = id }); }, "Primary"));
        top.Children.Add(B("启用群", () => GroupActionAsync("group-enable")));
        top.Children.Add(B("停用群", () => GroupActionAsync("group-disable")));
        top.Children.Add(B("重新核验群清理", () => GroupActionAsync("cleanup-retry")));
        Put(g, top, 0);
        var actions = Flow();
        _memberSearch = Input("按用户 ID 搜索", 260);
        _memberSearch.TextChanged += (_, _) => FilterMembers();
        actions.Children.Add(_memberSearch);
        actions.Children.Add(B("添加成员", async () => { var group = Selected(_memberGroup); if (group == "") return; var id = TextPrompt.Ask(this, Localization.T("添加普通成员"), Localization.T("成员稳定用户 ID")); if (id != null) await MutationAsync(new() { ["Action"] = "add", ["Group"] = group, ["User"] = id }); }, "Primary"));
        actions.Children.Add(B("启用成员", () => MemberActionAsync("enable", _memberTable)));
        actions.Children.Add(B("停用成员", () => MemberActionAsync("disable", _memberTable)));
        Put(g, actions, 1);
        _memberTable = Table(("用户 ID", "User", -2), ("身份 / 状态", "Status", 120), ("当前访问", "Access", 120));
        _memberTable.ItemsSource = _members;
        Put(g, _memberTable, 2);
        _groupSummary = T("本机机主是唯一全局管理员，不能被停用或删除。", 12, false, true);
        _groupSummary.Margin = new Thickness(0, 16, 0, 0);
        Put(g, _groupSummary, 3);
        return page;
    }
    FrameworkElement BuildDisabled()
    {
        var page = Page("已停用用户", "重新启用沿用保留数据；删除必须核对后端预览。");
        var g = G("Auto", "*", "Auto", "Auto");
        Put(page, g, 1);
        var filters = Flow();
        _disabledGroup = new ComboBox();
        _disabledGroup.SelectionChanged += (_, _) => { if (!_rosterChanging) FilterMembers(); };
        _disabledSearch = Input("按群 ID 或用户 ID 搜索");
        _disabledSearch.TextChanged += (_, _) => FilterMembers();
        filters.Children.Add(Field("群范围", _disabledGroup, 280));
        filters.Children.Add(Field("搜索", _disabledSearch, 320));
        Put(g, filters, 0);
        _disabledTable = Table(("群 ID", "Group", -1), ("用户 ID", "User", -1), ("生命周期", "Status", 120), ("所属群", "GroupStatus", 100));
        _disabledTable.ItemsSource = _disabled;
        Put(g, _disabledTable, 1);
        var actions = Flow();
        actions.Margin = new Thickness(0, 18, 0, 14);
        actions.Children.Add(B("重新启用", () => MemberActionAsync("enable", _disabledTable), "Primary"));
        actions.Children.Add(B("重新核验清理", () => MemberActionAsync("cleanup-retry", _disabledTable)));
        actions.Children.Add(B("删除当前群用户", () => DeleteMemberAsync(false), "Danger"));
        actions.Children.Add(B("从全部群删除", () => DeleteMemberAsync(true), "Danger"));
        Put(g, actions, 2);
        Put(g, T("删除包含私人文件、独立依赖、所属会话及成员记录；公共资料、其他用户和共享凭据保留。\n清理失败：核验后再启用；删除失败：重新预览并完成删除。", 12, false, true), 3);
        return page;
    }
    void UpdateRosterViews()
    {
        var selectedMember = (_memberTable.SelectedItem as ViewRow)?.Key;
        var selectedDisabled = (_disabledTable.SelectedItem as ViewRow)?.Key;
        _rosterChanging = true;
        try
        {
            var groups = Groups.ToList();
            var choices = groups.Select(g => new Choice(g.S("ID"), g.S("ID") + (g.B("Enabled") ? Localization.T(" · 已启用") : Localization.T(" · 已停用")))).ToList();
            Choices(_memberGroup, choices);
            Choices(_disabledGroup, new[] { new Choice("", Localization.T("全部群")) }.Concat(choices));
            var rows = new List<JsonNode>();
            foreach (var group in groups)
            {
                var gid = group.S("ID");
                rows.Add(new JsonObject { ["Group"] = gid, ["User"] = _owner, ["IsOwner"] = true, ["Enabled"] = true, ["State"] = Localization.T("全局管理员"), ["GroupEnabled"] = group.B("Enabled") });
                foreach (var member in group.Rows("Members"))
                {
                    var n = member.DeepClone();
                    n["Group"] = gid;
                    n["User"] = member.S("ID");
                    n["GroupEnabled"] = group.B("Enabled");
                    rows.Add(n);
                }
            }
            Sync(_members, rows, n => n.S("Group") + "\0" + n.S("User"));
            Sync(_disabled, rows.Where(n => !n.B("IsOwner") && !n.B("Enabled")), n => n.S("Group") + "\0" + n.S("User"));
        }
        finally { _rosterChanging = false; }
        FilterMembers();
        if (selectedMember != null)
            _memberTable.SelectedItem = _memberTable.Items.OfType<ViewRow>().FirstOrDefault(row => row.Key == selectedMember);
        if (selectedDisabled != null)
            _disabledTable.SelectedItem = _disabledTable.Items.OfType<ViewRow>().FirstOrDefault(row => row.Key == selectedDisabled);
        UpdateGroupChoices();
    }
    void FilterMembers()
    {
        if (_memberTable == null)
            return;
        CollectionViewSource.GetDefaultView(_members).Filter = o => o is ViewRow r && r.Group == Selected(_memberGroup) && r.User.Contains(_memberSearch.Text, StringComparison.OrdinalIgnoreCase);
        CollectionViewSource.GetDefaultView(_disabled).Filter = o => o is ViewRow r && (Selected(_disabledGroup) == "" || r.Group == Selected(_disabledGroup)) && (r.Group + " " + r.User).Contains(_disabledSearch.Text, StringComparison.OrdinalIgnoreCase);
        var group = Groups.FirstOrDefault(n => n.S("ID") == Selected(_memberGroup));
        _groupSummary.Text = Localization.F($"本机管理员：{_owner}（不可停用或删除）。 当前群：{J.State(group.S("State") is { Length: > 0 } s ? s : group.B("Enabled") ? "enabled" : "disabled")}。\n群停用不会改变成员自己的启停偏好；重新启用成员不会自动启用群。");
    }
    async Task GroupActionAsync(string action)
    {
        var id = Selected(_memberGroup);
        if (id == "")
            throw new InvalidOperationException(Localization.T("请选择群。"));
        if (action == "group-disable" && !Confirm(Localization.T("停用此群"), Localization.F($"群：{id}\n撤销本群访问、停止本群任务并取消待确认/审批。文档、依赖和成员偏好保留。")))
            return;
        await MutationAsync(new()
        {
            ["Action"] = action,
            ["Group"] = id
        });
    }
    async Task MemberActionAsync(string action, DataGrid table)
    {
        if (table.SelectedItem is not ViewRow r)
            throw new InvalidOperationException(Localization.T("请先选择普通成员。"));
        if (r.IsOwner)
            throw new InvalidOperationException(Localization.T("全局管理员不能被停用或删除。"));
        if (action == "disable" && !Confirm(Localization.T("停用普通成员"), Localization.F($"群：{r.Group}\n用户：{r.User}\n将立即撤权，停止排队及运行任务，取消待审批和待确认操作；文件、依赖和会话保留。")))
            return;
        await MutationAsync(new()
        {
            ["Action"] = action,
            ["Group"] = r.Group,
            ["User"] = r.User
        });
    }
    async Task DeleteMemberAsync(bool all)
    {
        if (_disabledTable.SelectedItem is not ViewRow r)
            throw new InvalidOperationException(Localization.T("请选择已停用成员。"));
        var request = new JsonObject { ["Action"] = "delete-preview", ["Version"] = _version, ["User"] = r.User };
        if (all)
            request["Scope"] = "all";
        else
            request["Group"] = r.Group;
        var reply = await Client.SendAsync(request);
        Check(reply);
        var p = reply["DeletePreview"] ?? throw new InvalidOperationException(Localization.T("后端未返回删除预览。"));
        var affected = string.Join("\n", p.Rows("Actors").Select(a => Localization.F($"群：{a.S("Group")}  用户：{a.S("User").Replace("wecom:", "")}")));
        var body = Localization.F($"{affected}\n\n私人文件及独立依赖合计：{p.N("Files")} 个文件 / {J.Bytes(p.N("Bytes"))}\nCodex 会话：{p.N("Threads")} 个\n\n将删除上述私人数据与成员记录。公共资料、其他用户和共享凭据保留。\n确认有效至：{J.Time(p.S("Expires"))}。文件或配置变化后需重新预览。");
        if (!Confirm(all ? Localization.T("从全部群彻底删除用户") : Localization.T("删除当前群中的用户"), body))
            return;
        request["Action"] = "delete";
        request["Confirm"] = p.S("Token");
        var deleted = await Client.SendAsync(request);
        await RefreshCoreAsync(false);
        Check(deleted);
        Notice(Localization.T("后端已确认删除完成；公共资料保留。"));
    }
}
