using System.Runtime.InteropServices;
using System.Windows;
using System.Windows.Controls;
using System.Windows.Interop;

namespace Tongxu.Desktop;

// Uses the Windows shell already available on this WPF target; no extra package.
internal sealed class TrayIcon : IDisposable
{
    const int Callback = 0x8001;
    readonly Window _window;
    readonly Action _open, _exit;
    readonly HwndSource _source;
    readonly uint _taskbarCreated = RegisterWindowMessage("TaskbarCreated");
    NotifyData _data;
    bool _visible;
    internal TrayIcon(Window window, Action open, Action exit)
    {
        _window = window; _open = open; _exit = exit;
        var hwnd = new WindowInteropHelper(window).EnsureHandle();
        _source = HwndSource.FromHwnd(hwnd);
        var icon = SendMessage(hwnd, 0x007F, 1, 0); // WM_GETICON, ICON_BIG
        if (icon == 0) icon = SendMessage(hwnd, 0x007F, 0, 0);
        _data = new NotifyData { Size = Marshal.SizeOf<NotifyData>(), Window = hwnd, ID = 1,
            Flags = 1 | 2 | 4, CallbackMessage = Callback, Icon = CopyIcon(icon),
            Tip = "同序 · tongxu", Info = "", InfoTitle = "" };
        if (_data.Icon == 0) throw new InvalidOperationException("Unable to create tray icon.");
        _source.AddHook(Hook);
    }
    internal void Show()
    {
        if (_visible) return;
        if (!Shell_NotifyIcon(0, ref _data)) throw new InvalidOperationException("Unable to show tray icon.");
        _visible = true;
    }
    nint Hook(nint hwnd, int message, nint wParam, nint lParam, ref bool handled)
    {
        if ((uint)message == _taskbarCreated && _visible)
        {
            _visible = false;
            try { Show(); } catch { _open(); }
        }
        if (message == Callback)
        {
            handled = true;
            if ((int)lParam == 0x0202) _open(); // left button up
            if ((int)lParam == 0x0205) // right button up
            {
                SetForegroundWindow(hwnd);
                var menu = new ContextMenu { Style = (Style)Application.Current.FindResource("TrayMenu") };
                var itemStyle = (Style)Application.Current.FindResource("TrayMenuItem");
                var open = new MenuItem { Header = Localization.T("打开主界面"), Style = itemStyle };
                open.Click += (_, _) => _open();
                var exit = new MenuItem { Header = Localization.T("退出"), Style = itemStyle };
                exit.Click += (_, _) => _exit();
                menu.Items.Add(open); menu.Items.Add(exit);
                menu.PlacementTarget = _window;
                menu.IsOpen = true;
            }
        }
        return 0;
    }
    public void Dispose()
    {
        if (_visible) Shell_NotifyIcon(2, ref _data);
        _visible = false;
        _source.RemoveHook(Hook);
        if (_data.Icon != 0) { DestroyIcon(_data.Icon); _data.Icon = 0; }
    }
    [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
    struct NotifyData
    {
        public int Size; public nint Window; public uint ID, Flags; public int CallbackMessage; public nint Icon;
        [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 128)] public string Tip;
        public uint State, StateMask;
        [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 256)] public string Info;
        public uint Version;
        [MarshalAs(UnmanagedType.ByValTStr, SizeConst = 64)] public string InfoTitle;
        public uint InfoFlags; public Guid Guid; public nint BalloonIcon;
    }
    [DllImport("shell32.dll", CharSet = CharSet.Unicode)] static extern bool Shell_NotifyIcon(uint message, ref NotifyData data);
    [DllImport("user32.dll", CharSet = CharSet.Unicode)] static extern uint RegisterWindowMessage(string message);
    [DllImport("user32.dll")] static extern nint SendMessage(nint hwnd, uint message, nint wParam, nint lParam);
    [DllImport("user32.dll")] static extern nint CopyIcon(nint icon);
    [DllImport("user32.dll")] static extern bool DestroyIcon(nint icon);
    [DllImport("user32.dll")] static extern bool SetForegroundWindow(nint hwnd);
}
