using System.IO;
using System.Windows;
using System.Windows.Controls;
using System.Windows.Media;
using System.Windows.Threading;

namespace Tongxu.Desktop;

public partial class MainWindow
{
    internal async Task VerifyUpdateSettingsAsync(string evidence)
    {
        await NavigateAsync(5);
        if (!_checkUpdate.IsEnabled || _downloadUpdate.IsEnabled || _installUpdate.IsEnabled || _cancelUpdate.IsEnabled)
            throw new InvalidOperationException("Incorrect initial update buttons.");
        _updates = new UpdateService(Client.Runtime);
        _release = new(new Version(9,0,0), "v9.0.0", "Synthetic update notes only. No network, installer or real release used.", new Uri("https://github.com/iwanttobeacmer666-ctrl/TONGXU/releases/download/v9.0.0/Tongxu-9.0.0-win-x64.msi"), 8, new string('0',64));
        ShowRelease();
        if (!_downloadUpdate.IsEnabled || !_skipUpdate.IsEnabled || _installUpdate.IsEnabled) throw new InvalidOperationException("Available-release buttons incorrect.");
        await SkipUpdateAsync();
        if (_updates.LoadState().SkippedVersion != "9.0.0") throw new InvalidOperationException("Skip not saved.");
        _updatePackage = Path.Combine(Client.Runtime,"not-an-installer.msi");
        UpdateButtons();
        if (!_installUpdate.IsEnabled || _downloadUpdate.IsEnabled) throw new InvalidOperationException("Pending-release buttons incorrect.");
        await InstallUpdateAsync();
        if (_updateBusy || _closing || _updateStatus.Text != Localization.T("当前为开发版目录；安装包完成后，安装版才能自动升级。")) throw new InvalidOperationException("Development deployment did not block installer execution.");
        ScrollViewer? FindScroll(DependencyObject parent)
        {
            if (parent is ScrollViewer found) return found;
            for (int i = 0; i < VisualTreeHelper.GetChildrenCount(parent); i++)
                if (FindScroll(VisualTreeHelper.GetChild(parent,i)) is { } child) return child;
            return null;
        }
        var scroll = FindScroll(_pages[5]);
        Width = 1040; Height = 740;
        Show(); // Existing fixture positions this window off-screen.
        await Dispatcher.Yield(DispatcherPriority.ContextIdle);
        scroll?.ScrollToBottom();
        await Dispatcher.Yield(DispatcherPriority.ContextIdle);
        DesktopVerification.Capture(this,Path.Combine(evidence,"desktop-updates.png"),1040,740);
        Hide();
        _release = null; _updatePackage = null; ShowRelease();
    }
}
