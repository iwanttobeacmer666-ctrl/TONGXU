using System.Windows;
using System.Windows.Controls;

namespace Tongxu.Desktop;

public static class TextPrompt
{
    public static string? Ask(Window owner, string title, string label)
    {
        var window = new Window { Owner = owner, Title = title, Width = 540, SizeToContent = SizeToContent.Height, ResizeMode = ResizeMode.NoResize, WindowStartupLocation = WindowStartupLocation.CenterOwner };
        var stack = new StackPanel { Margin = new Thickness(28) };
        stack.Children.Add(new TextBlock { Text = label, Margin = new Thickness(0, 0, 0, 12) });
        var input = new TextBox();
        stack.Children.Add(input);
        var row = new StackPanel { Orientation = Orientation.Horizontal, Margin = new Thickness(0, 18, 0, 0) };
        var ok = new Button { Content = Localization.T("确定"), IsDefault = true, Style = (Style)Application.Current.FindResource("Primary") };
        var cancel = new Button { Content = Localization.T("取消"), IsCancel = true };
        ok.Click += (_, _) => { if (!string.IsNullOrWhiteSpace(input.Text)) window.DialogResult = true; };
        row.Children.Add(ok);
        row.Children.Add(cancel);
        stack.Children.Add(row);
        WindowCaption.Attach(window, stack);
        window.Loaded += (_, _) => input.Focus();
        return window.ShowDialog() == true ? input.Text.Trim() : null;
    }
}
