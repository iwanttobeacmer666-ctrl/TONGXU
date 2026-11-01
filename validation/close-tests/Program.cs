using System.Diagnostics;
using System.IO;
using System.Reflection;
using System.Runtime.InteropServices;
using System.Text;
using System.Windows;
using System.Windows.Threading;
using Tongxu.Desktop;

internal static class Program
{
    static readonly BindingFlags Private = BindingFlags.Instance | BindingFlags.NonPublic;
    static int checks;
    static int exitCode;
    static void Check(bool value, string message)
    {
        if (!value) throw new InvalidOperationException(message);
        Console.WriteLine("PASS: " + message); checks++;
    }
    static object? Field(MainWindow window, string name) => typeof(MainWindow).GetField(name, Private)!.GetValue(window);
    static void Set(MainWindow window, string name, object value) => typeof(MainWindow).GetField(name, Private)!.SetValue(window, value);

    [STAThread]
    static int Main(string[] args)
    {
        var app = new Application { ShutdownMode = ShutdownMode.OnExplicitShutdown };
        app.Resources.MergedDictionaries.Add(new ResourceDictionary { Source = new Uri("/tongxu;component/Styles.xaml", UriKind.Relative) });
        app.Resources["TongxuIcon"] = new System.Windows.Media.Imaging.BitmapImage(new Uri("pack://application:,,,/tongxu;component/Assets/tongxu.png"));
        typeof(MainWindow).Assembly.GetType("Tongxu.Desktop.Theme")!.GetMethod("Apply", BindingFlags.Static | BindingFlags.NonPublic)!.Invoke(null, ["light", null]);
        app.Dispatcher.BeginInvoke(async () =>
        {
            try
            {
                await Run(args[0], args.Length > 1 ? args[1] : null);
                Console.WriteLine($"PASS: {checks} close regression checks. Isolated fixtures only; no real service or credentials used.");
            }
            catch (Exception ex) { Console.Error.WriteLine(ex); exitCode = 1; }
            finally { app.Shutdown(); }
        });
        app.Run();
        return exitCode;
    }
    static async Task Run(string root, string? backend)
    {
        var client = new LocalClient(root);
        var verification = typeof(MainWindow).GetProperty("IsVerification", Private)!;
        var thread = GetCurrentThreadId();
        async Task Case(string mode, bool yes, bool expectClose, bool tray = false, bool exit = false)
        {
            File.WriteAllText(Path.Combine(root, "bin", "mode.txt"), mode);
            var window = new MainWindow(client)
            {
                ShowInTaskbar = false, ShowActivated = false,
                WindowStartupLocation = WindowStartupLocation.Manual, Left = -20000, Top = -20000
            };
            verification.SetValue(window, true);
            window.Show();
            await Dispatcher.Yield(DispatcherPriority.ContextIdle);
            verification.SetValue(window, false);
            bool closed = false;
            window.Closed += (_, _) => closed = true;
            int prompts = 0;
            bool defaultNo = true;
            var timer = new DispatcherTimer { Interval = TimeSpan.FromMilliseconds(30) };
            timer.Tick += (_, _) => EnumThreadWindows(thread, (hwnd, _) =>
            {
                var name = new StringBuilder(64); GetClassName(hwnd, name, name.Capacity);
                if (name.ToString() != "#32770") return true;
                prompts++;
                defaultNo &= ((long)SendMessage(hwnd, 0x0400, 0, 0) & 0xffff) == 7;
                SendMessage(hwnd, 0x0111, yes ? 6 : 7, 0); // This test process's Yes/No dialog only.
                return true;
            }, 0);
            timer.Start();
            var clock = Stopwatch.StartNew();
            try
            {
                Task<bool>? exiting = null;
                if (exit) exiting = (Task<bool>)typeof(MainWindow).GetMethod("ExitFromTrayAsync", Private)!.Invoke(window, null)!;
                else window.Close(); // Actual WPF Closing event, not a policy-only test.
                while (!(closed || tray && !window.IsVisible || prompts > 0 && !(bool)Field(window, exit ? "_exitPending" : "_closePending")!))
                {
                    if (clock.Elapsed > TimeSpan.FromSeconds(8)) throw new TimeoutException($"{mode}: close did not finish or offer an escape");
                    await Task.Delay(30);
                }
                if (exiting != null) Check(await exiting == expectClose, mode + ": exit result");
                Check(closed == expectClose, mode + ": window close decision");
                bool uncertain = mode is not "offline" and not "online";
                Check(prompts == (uncertain ? 1 : 0), mode + ": confirmation only when state is uncertain");
                Check(defaultNo, mode + ": uncertain close defaults to No");
                if (tray) Check(Field(window, "_tray") != null && !closed && !window.IsVisible, "online close preserves a real tray entry");
                if (mode == "slow") Check(clock.Elapsed < TimeSpan.FromSeconds(6), "stalled status is bounded before close confirmation");
                if (!expectClose) Check(!(bool)Field(window, "_closing")!, mode + ": cancel preserves the window lifecycle");
                if (exit) Check(((SemaphoreSlim)Field(window, "_work")!).CurrentCount == 1, "exit releases the work lock");
            }
            finally { timer.Stop(); if (!closed) { Set(window, "_closing", true); window.Close(); } }
        }
        await Case("offline", true, true);
        await Case("online", true, false, tray: true);
        await Case("unconfigured", false, false);
        await Case("unconfigured", true, true);
        await Case("invalid-json", true, true);
        await Case("missing", true, true);
        await Case("slow", true, true);
        await Case("unconfigured", false, false, exit: true);
        await Case("unconfigured", true, true, exit: true);
        var actions = File.ReadAllLines(Path.Combine(root, "bin", "requests.txt"));
        Check(actions.All(a => a == "{\"Action\":\"status\"}"), "uncertain close never sends service-stop or replays a mutation");
        Check(!File.Exists(client.OperatorSettings), "first-run close does not create operator settings");
        if (backend != null)
        {
            File.Copy(backend, client.Binary, true); // Replace only this test's synthetic broker.
            Directory.CreateDirectory(client.Runtime);
            var status = await client.SendAsync(new() { ["Action"] = "status" });
            Check(status["Success"]!.GetValue<bool>() && !status["Online"]!.GetValue<bool>() && status["Status"]!["Service"]!.GetValue<string>() == "offline", "real unconfigured backend reports offline");
            await Case("offline", false, true);
            Check(!File.Exists(client.OperatorSettings), "real first-run close needs no configuration or fallback prompt");
        }
    }
    delegate bool EnumWindow(nint hwnd, nint parameter);
    [DllImport("kernel32.dll")] static extern uint GetCurrentThreadId();
    [DllImport("user32.dll")] static extern bool EnumThreadWindows(uint thread, EnumWindow callback, nint parameter);
    [DllImport("user32.dll", CharSet = CharSet.Unicode)] static extern int GetClassName(nint hwnd, StringBuilder name, int length);
    [DllImport("user32.dll")] static extern nint SendMessage(nint hwnd, uint message, nint wParam, nint lParam);
}
