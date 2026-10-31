using System.Text.Json.Nodes;
using System.Windows;

namespace Tongxu.Desktop;

public partial class MainWindow
{
    TrayIcon? _tray;
    bool _closePending, _exitPending;

    void OpenFromTray()
    {
        Show();
        if (WindowState == WindowState.Minimized) WindowState = WindowState.Normal;
        Activate();
    }
    async Task HandleWindowCloseAsync()
    {
        if (_closePending || _exitPending || _updateInstalling) return;
        if (_updateBusy) { OpenFromTray(); Notice(Localization.T("请先取消更新操作，或等待更新完成。")); return; }
        _closePending = true;
        try
        {
            // Pending launch/commands may still create or stop the service.
            // Keep the UI reachable while their result is unsettled.
            bool keepAlive = _commandPending || _launcher is { IsCompleted: false };
            if (!keepAlive)
            {
                var status = await Client.SendAsync(new() { ["Action"] = "status" });
                Check(status);
                keepAlive = status.B("Online");
            }
            if (keepAlive)
            {
                _tray ??= new TrayIcon(this, OpenFromTray, () => _ = ExitFromTrayAsync());
                _tray.Show(); // Only hide after the shell confirms a reachable icon.
                Hide();
            }
            else { _closing = true; Close(); }
        }
        catch (Exception ex) { OpenFromTray(); Notice(Localization.T("当前服务状态未确认。") + SafeText.Clean(ex.Message), true); }
        finally { _closePending = false; }
    }
    internal async Task<bool> ExitFromTrayAsync()
    {
        if (_exitPending || _updateBusy) return false;
        _exitPending = true;
        _poll.Stop();
        OpenFromTray();
        _content.IsEnabled = false;
        _progress.Visibility = Visibility.Visible;
        Notice(Localization.T("正在停止后台服务并退出…"));
        await _work.WaitAsync();
        try
        {
            // A startup check may not yet have opened the broker. Continue until
            // both the broker is offline and our launch helper has exited.
            using var deadline = new CancellationTokenSource(TimeSpan.FromSeconds(90));
            bool stopSent = false, stopping = false;
            while (true)
            {
                JsonNode status;
                try
                {
                    status = await Client.SendAsync(new() { ["Action"] = "status" }, deadline.Token);
                    Check(status);
                }
                catch (Exception) when (!deadline.IsCancellationRequested && (stopSent || stopping || _launcher is { IsCompleted: false }))
                {
                    // The broker closes before task cleanup releases the service
                    // lock. Retry only the status read, never the stop command.
                    await Task.Delay(250, deadline.Token);
                    continue;
                }
                stopping = status["Status"].S("Service") == "stopping";
                if (!status.B("Online") && _launcher is not { IsCompleted: false }) break;
                if (status.B("Online") && !stopSent && status["Status"].S("Service") != "stopping")
                {
                    var roster = await Client.SendAsync(new() { ["Action"] = "list" }, deadline.Token);
                    Check(roster);
                    var reply = await Client.SendAsync(new() { ["Action"] = "service-stop", ["Version"] = roster.S("Version") }, deadline.Token);
                    Check(reply);
                    stopSent = true;
                }
                await Task.Delay(250, deadline.Token);
            }
            _closing = true;
            if (!IsVerification) Close();
            return true;
        }
        catch (Exception ex)
        {
            Notice(Localization.T("退出未完成，请核对服务状态后重试。") + SafeText.Clean(ex.Message), true);
            return false;
        }
        finally
        {
            _work.Release(); _exitPending = false;
            _content.IsEnabled = true; _progress.Visibility = Visibility.Collapsed;
            if (!_closing && !IsVerification) _poll.Start();
        }
    }
}
