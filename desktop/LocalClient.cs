using System.Diagnostics;
using System.IO;
using System.Text;
using System.Text.Json;
using System.Text.Json.Nodes;

namespace Tongxu.Desktop;

public sealed class LocalClient(string root)
{
    public string Root { get; } = Path.GetFullPath(root);
    public string Binary => Path.Combine(Root, "bin", "group-assistant.exe");
    public string Runtime => AppPaths.Runtime(Root);
    public string OperatorSettings => Path.Combine(Runtime, "operator-setup.json");
    public Task<JsonNode> SendAsync(JsonObject request, CancellationToken token = default) => ExecuteAsync(Binary, ["-manage-local", "-operator-settings", OperatorSettings], request, token);
    public Task<JsonNode> SettingsAsync(string action, JsonObject? request = null, CancellationToken token = default) => ExecuteAsync(Shell, ["-NoProfile", "-NonInteractive", "-File", Path.Combine(Root, "Desktop-Operator.ps1"), "-Action", action, "-ProjectRoot", Root], request, token);
    public static string Shell => Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.System), "WindowsPowerShell", "v1.0", "powershell.exe");
    static async Task<string> ReadAsync(StreamReader reader, int max, bool discard = false)
    {
        var result = new StringBuilder();
        var buffer = new char[4096];
        int n;
        while ((n = await reader.ReadAsync(buffer)) > 0)
            if (!discard && result.Length < max)
                result.Append(buffer, 0, Math.Min(n, max - result.Length));
        return result.ToString();
    }
    async Task<JsonNode> ExecuteAsync(string exe, string[] args, JsonObject? request, CancellationToken token)
    {
        using var timeout = CancellationTokenSource.CreateLinkedTokenSource(token);
        timeout.CancelAfter(TimeSpan.FromSeconds(45));
        using var process = Start(exe, args);
        var stdout = ReadAsync(process.StandardOutput, 262145);
        var stderr = ReadAsync(process.StandardError, 0, true);
        try
        {
            if (request != null)
            {
                var bytes = Encoding.UTF8.GetBytes(request.ToJsonString());
                if (bytes.Length > 65536)
                    throw new InvalidOperationException(Localization.T("本次选择过大，请分批操作。"));
                await process.StandardInput.BaseStream.WriteAsync(bytes, timeout.Token);
            }
            process.StandardInput.Close();
            await process.WaitForExitAsync(timeout.Token);
            await stderr;
            var text = await stdout;
            if (text.Length > 262144)
                throw new InvalidOperationException(Localization.T("响应过大，请缩小范围并刷新核对。"));
            var reply = JsonNode.Parse(text) ?? throw new InvalidOperationException();
            return reply;
        }
        catch (OperationCanceledException) { if (!process.HasExited) process.Kill(); throw new InvalidOperationException(Localization.T("等待超时或已取消，操作结果尚未确认。请刷新核对，不要直接重复提交。")); }
        catch (JsonException) { throw new InvalidOperationException(Localization.T("未收到可确认的结果。请刷新核对；不会自动重试。")); }
        finally { if (!process.HasExited) process.Kill(); }
    }
    Process Start(string exe, string[] args)
    {
        var start = new ProcessStartInfo(exe) { UseShellExecute = false, CreateNoWindow = true, WorkingDirectory = Root, RedirectStandardInput = true, RedirectStandardOutput = true, RedirectStandardError = true, StandardOutputEncoding = new UTF8Encoding(false), StandardErrorEncoding = new UTF8Encoding(false) };
        foreach (var a in args)
            start.ArgumentList.Add(a);
        return Process.Start(start) ?? throw new InvalidOperationException(Localization.T("无法运行现有本地程序。"));
    }
    public async Task<JsonNode> LaunchAsync()
    {
        using var process = Start(Shell, ["-NoProfile", "-NonInteractive", "-File", Path.Combine(Root, "Desktop-Operator.ps1"), "-Action", "launch", "-ProjectRoot", Root]);
        process.StandardInput.Close();
        var output = ReadAsync(process.StandardOutput, 262144);
        var errors = ReadAsync(process.StandardError, 0, true);
        await process.WaitForExitAsync();
        await errors;
        try
        {
            return JsonNode.Parse(await output) ?? new JsonObject { ["Success"] = false, ["Message"] = Localization.T("服务退出，未确认清理状态。") };
        }
        catch (JsonException) { return new JsonObject { ["Success"] = false, ["Message"] = Localization.T("启动或退出状态未确认，请刷新核对配置与服务。") }; }
    }
    public void Login()
    {
        var start = new ProcessStartInfo(Shell) { UseShellExecute = true, WorkingDirectory = Root };
        foreach (var arg in new[] { "-NoProfile", "-NoExit", "-File", Path.Combine(Root, "Desktop-Operator.ps1"), "-Action", "login", "-ProjectRoot", Root })
            start.ArgumentList.Add(arg);
        Process.Start(start);
    }
}
