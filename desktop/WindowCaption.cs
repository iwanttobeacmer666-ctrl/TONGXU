using System.Windows;
using System.Windows.Automation;
using System.Windows.Controls;
using System.Windows.Data;
using System.Windows.Media;
using System.Windows.Shell;

namespace Tongxu.Desktop;

internal static class WindowCaption
{
    internal const double Height = 40;
    public static Grid Attach(Window window, UIElement body, Grid? canvas = null, double sidebarWidth = 0)
    {
        window.SetResourceReference(Control.BackgroundProperty, "Surface");
        window.SetResourceReference(Control.ForegroundProperty, "Ink");
        window.WindowStyle = WindowStyle.None;
        window.BorderThickness = new Thickness(0);
        window.Icon = (ImageSource)Application.Current.FindResource("TongxuIcon");
        WindowChrome.SetWindowChrome(window, new WindowChrome { CaptionHeight = Height, ResizeBorderThickness = new Thickness(window.ResizeMode == ResizeMode.NoResize ? 0 : 6), GlassFrameThickness = new Thickness(0), CornerRadius = new CornerRadius(0), UseAeroCaptionButtons = false });
        Localization.ApplyTo(window);
        var shell = canvas ?? new Grid { Background = window.Background };
        if (canvas is null)
            window.Content = null;
        shell.RowDefinitions.Add(new()
        {
            Height = new GridLength(Height)
        });
        shell.RowDefinitions.Add(new());
        Grid.SetRow(body, 1);
        shell.Children.Add(body);
        var caption = new Grid { Background = Theme.Get("Overlay") };
        if (sidebarWidth > 0)
        {
            var side = new Border { Background = Theme.Get("Sidebar"), HorizontalAlignment = HorizontalAlignment.Left, Width = sidebarWidth };
            caption.Children.Add(side);
            shell.SizeChanged += (_, e) => side.Width = e.NewSize.Width < 1100 ? 190 : 224;
        }
        var title = new TextBlock { Margin = new Thickness(22, 0, 150, 0), FontSize = 12, Foreground = (Brush)Application.Current.FindResource("Muted"), TextTrimming = TextTrimming.CharacterEllipsis, TextWrapping = TextWrapping.NoWrap };
        title.SetBinding(TextBlock.TextProperty, new Binding(nameof(Window.Title)) { Source = window });
        caption.Children.Add(title);
        var controls = new StackPanel { Orientation = Orientation.Horizontal, HorizontalAlignment = HorizontalAlignment.Right };
        Button Add(string glyph, string label, Action execute, bool close = false)
        {
            var button = new Button { Content = glyph, Width = 46, Height = Height, MinHeight = 0, Padding = new Thickness(0), Margin = new Thickness(0), FontFamily = new FontFamily("Segoe MDL2 Assets"), FontSize = 10, Style = (Style)Application.Current.FindResource(close ? "CaptionClose" : "CaptionButton") };
            Localization.Bind(button, FrameworkElement.ToolTipProperty, label);
            Localization.Bind(button, AutomationProperties.NameProperty, label);
            WindowChrome.SetIsHitTestVisibleInChrome(button, true);
            button.Click += (_, _) => execute();
            controls.Children.Add(button);
            return button;
        }
        if (window.ResizeMode is ResizeMode.CanResize or ResizeMode.CanResizeWithGrip or ResizeMode.CanMinimize)
            Add("\uE921", "最小化", () => SystemCommands.MinimizeWindow(window));
        if (window.ResizeMode is ResizeMode.CanResize or ResizeMode.CanResizeWithGrip)
        {
            var maximize = Add("\uE922", "最大化", () => { if (window.WindowState == WindowState.Maximized) SystemCommands.RestoreWindow(window); else SystemCommands.MaximizeWindow(window); });
            window.StateChanged += (_, _) =>
            {
                maximize.Content = window.WindowState == WindowState.Maximized ? "\uE923" : "\uE922";
                var label = window.WindowState == WindowState.Maximized ? "还原窗口" : "最大化";
                Localization.Bind(maximize, FrameworkElement.ToolTipProperty, label);
                Localization.Bind(maximize, AutomationProperties.NameProperty, label);
            };
        }
        Add("\uE8BB", "关闭", () => SystemCommands.CloseWindow(window), true);
        caption.Children.Add(controls);
        shell.Children.Add(caption);
        window.Content = shell;
        return shell;
    }
}
