using System.ComponentModel;
using System.Runtime.CompilerServices;
using System.Text.Json.Nodes;
using System.Text.RegularExpressions;

namespace Tongxu.Desktop;

public static class SafeText
{
    public static string Clean(string? text) => Regex.Replace(Regex.Replace(text ?? "", @"(?i)(bearer|basic)\s+[^\s""']+|sk-[a-zA-Z0-9_-]{16,}", Localization.T("[已隐藏]")), @"(?i)((?:secret|password|api[_-]?key|access[_-]?token|authorization)\s*[=:]\s*)[^\r\n,;]+", Localization.T("$1[已隐藏]"));
}
public static class J
{
    public static string S(this JsonNode? n, string key) => n?[key]?.ToString() ?? "";
    public static bool B(this JsonNode? n, string key) => bool.TryParse(n.S(key), out var v) && v;
    public static long N(this JsonNode? n, string key) => long.TryParse(n.S(key), out var v) ? v : 0;
    public static IEnumerable<JsonNode> Rows(this JsonNode? n, string key) => (n?[key] as JsonArray)?.OfType<JsonNode>() ?? [];
    public static string Bytes(long n) => n >= 1L << 30 ? $"{n / (double)(1L << 30):0.##} GB" : n >= 1L << 20 ? $"{n / (double)(1L << 20):0.##} MB" : n >= 1024 ? $"{n / 1024d:0.##} KB" : $"{n} B";
    public static string Time(string v) => DateTimeOffset.TryParse(v, out var d) && d.Year > 1 ? d.ToLocalTime().ToString("MM-dd HH:mm:ss") : "—";
    public static string State(string state) => state switch { "enabled" => Localization.T("已启用"), "disabled" => Localization.T("已停用"), "disabling" => Localization.T("停用中"), "cleanup_failed" => Localization.T("清理失败"), "deleting" => Localization.T("删除中"), "delete_failed" => Localization.T("删除失败"), "queued" => Localization.T("排队中"), "downloading" => Localization.T("下载中"), "running" => Localization.T("运行中"), "waiting_approval" => Localization.T("等待审批"), "publishing" => Localization.T("发布中"), "stopping" => Localization.T("停止中"), "stopped" => Localization.T("已停止"), "completed" => Localization.T("已完成"), "failed" => Localization.T("失败"), _ => state };
}
public class ViewRow : INotifyPropertyChanged
{
    public string Key { get; set; } = "";
    JsonNode? _data;
    public JsonNode? Data
    {
        get => _data; set
        {
            _data = value;
            PropertyChanged?.Invoke(this, new PropertyChangedEventArgs(""));
        }
    }
    public string Group => Data.S("Group"); public string User => Data.S("User"); public string ID => Data.S("ID");
    public string Name => Data.S("Name"); public string Size => J.Bytes(Data.N("Bytes"));
    public bool Enabled => Data.B("Enabled"); public bool IsOwner => Data.B("IsOwner");
    public string State => Data.S("State") is { Length: > 0 } s ? s : Enabled ? "enabled" : "disabled";
    public string Status => J.State(State); public string GroupStatus => Data.B("GroupEnabled") ? Localization.T("群已启用") : Localization.T("群已停用");
    public System.Windows.Media.Brush StatusColor => Theme.Get(State switch { "cleanup_failed" or "delete_failed" or "failed" => "Error", "disabling" or "deleting" or "stopping" or "waiting_approval" => "Warning", "enabled" or "running" or "completed" => "Accent", _ => "Muted" });
    public string Access => IsOwner ? Localization.T("全局管理员") : Enabled && Data.B("GroupEnabled") ? Localization.T("可访问") : Localization.T("无访问权限");
    public string Created => J.Time(Data.S("Created")); public string Started => J.Time(Data.S("Started")); public string Ended => J.Time(Data.S("Ended"));
    public string TaskUser => Data?["Actor"].S("User").Replace("wecom:", "") ?? "";
    public string TaskGroup => Data?["Actor"].S("Group") ?? "";
    public string EventText => SafeText.Clean(Data.S("Text"));
    public string FileStatus => Data.B("Blocked") ? Localization.T("需管理员核查") : Data.B("Directory") ? Localization.T("文件夹") : Localization.T("文件");
    public event PropertyChangedEventHandler? PropertyChanged;
    internal void RefreshLanguage() => PropertyChanged?.Invoke(this, new PropertyChangedEventArgs(""));
}
public record Choice(string ID, string Label)
{
    public override string ToString() => Label;
}
