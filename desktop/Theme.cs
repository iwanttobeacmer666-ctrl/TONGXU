using System.Windows;
using System.Windows.Media;
using System.Windows.Data;
using System.ComponentModel;
using Microsoft.Win32;

namespace Tongxu.Desktop;

internal static class Theme
{
    internal static string Mode { get; private set; } = "system";
    internal static bool IsDark { get; private set; }
    // Shared mutable brushes also update controls created directly in C#.
    static readonly Dictionary<string, SolidColorBrush> Brushes = [];
    static readonly Dictionary<string, ThemeColor> Colors = [];
    static readonly (string Key, string Light, string Dark)[] Palette = [
        ("Accent", "#24694F", "#D4D4D4"), ("Ink", "#202A26", "#ECECEC"),
        ("Muted", "#6B7771", "#B0B0B0"), ("Line", "#DFE6E1", "#414141"),
        ("Canvas", "#F6F8F7", "#232323"), ("Surface", "#FFFFFF", "#181818"),
        ("Sidebar", "#F0F3F1", "#202020"), ("Overlay", "#E6FFFFFF", "#E6181818"),
        ("Selected", "#E7F1EB", "#383838"), ("Hover", "#E4EBE6", "#303030"),
        ("Pressed", "#CBD6CF", "#484848"), ("Error", "#AA3A3A", "#FF9D9D"),
        ("Warning", "#91620D", "#EAC06F"), ("OnAccent", "#FFFFFF", "#181818"),
        ("CloseHover", "#E8C7C7", "#643737")
    ];
    internal static SolidColorBrush Get(string key) => Brushes[key];
    internal static void Apply(string mode, bool? systemDark = null)
    {
        Mode = mode is "light" or "dark" ? mode : "system";
        IsDark = Mode == "dark" || Mode == "system" && (systemDark ?? ReadSystemDark());
        foreach (var (key, light, dark) in Palette)
        {
            var color = (Color)ColorConverter.ConvertFromString(IsDark ? dark : light);
            if (Colors.TryGetValue(key, out var source)) source.Value = color;
            else
            {
                source = Colors[key] = new ThemeColor { Value = color };
                var brush = new SolidColorBrush();
                // A bound Freezable cannot be frozen by WPF style sealing.
                BindingOperations.SetBinding(brush, SolidColorBrush.ColorProperty, new Binding(nameof(ThemeColor.Value)) { Source = source });
                Application.Current.Resources[key] = Brushes[key] = brush;
            }
        }
    }
    static bool ReadSystemDark()
    {
        try
        {
            using var key = Registry.CurrentUser.OpenSubKey(@"Software\Microsoft\Windows\CurrentVersion\Themes\Personalize");
            return key?.GetValue("AppsUseLightTheme") is int value && value == 0;
        }
        catch (System.Security.SecurityException) { return false; }
        catch (UnauthorizedAccessException) { return false; }
    }
    sealed class ThemeColor : INotifyPropertyChanged
    {
        Color _value;
        public Color Value
        {
            get => _value;
            set { _value = value; PropertyChanged?.Invoke(this, new(nameof(Value))); }
        }
        public event PropertyChangedEventHandler? PropertyChanged;
    }
}
