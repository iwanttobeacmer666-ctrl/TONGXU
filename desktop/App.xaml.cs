using System.IO;
using System.Windows;

namespace Tongxu.Desktop;

public partial class App : Application
{
    protected override async void OnStartup(StartupEventArgs e)
    {
        base.OnStartup(e);
        DispatcherUnhandledException += (_, a) => { MessageBox.Show(Localization.T("界面遇到异常，请刷新后核对操作结果。不会自动重试操作。"), Localization.T("同序")); a.Handled = true; };
        try
        {
            string? root = null, evidence = null;
            bool selfTest = false;
            for (int i = 0; i < e.Args.Length; i++)
                switch (e.Args[i])
                {
                    case "--project":
                        root = e.Args[++i];
                        break;
                    case "--self-test":
                        selfTest = true;
                        break;
                    case "--evidence":
                        evidence = e.Args[++i];
                        break;
                    default:
                        throw new InvalidOperationException(Localization.T("不支持的启动参数。"));
                }
            root = root is null ? FindRoot() : Path.GetFullPath(root);
            Localization.LoadPreference(root);
            Theme.Apply("system");
            var client = new LocalClient(root);
            if (selfTest)
            {
                await DesktopVerification.RunAsync(client, evidence);
                Shutdown(0);
                return;
            }
            var window = new MainWindow(client);
            MainWindow = window;
            window.Show();
        }
        catch (Exception ex) { if (e.Args.Contains("--self-test")) { if (e.Args.Contains("--evidence")) { var i = Array.IndexOf(e.Args, "--evidence"); Directory.CreateDirectory(e.Args[i + 1]); await File.WriteAllTextAsync(Path.Combine(e.Args[i + 1], "failure.txt"), ex.ToString()); } Shutdown(1); } else { MessageBox.Show(SafeText.Clean(ex.Message), Localization.T("同序启动失败")); Shutdown(1); } }
    }
    static string FindRoot()
    {
        for (var d = new DirectoryInfo(AppContext.BaseDirectory); d != null; d = d.Parent)
            if (File.Exists(Path.Combine(d.FullName, "Desktop-Operator.ps1")) && File.Exists(Path.Combine(d.FullName, "bin", "group-assistant.exe")))
                return d.FullName;
        throw new InvalidOperationException(Localization.T("未找到项目。请将桌面程序保留在项目 bin\\desktop 目录，或使用 --project 指定项目。"));
    }
}
