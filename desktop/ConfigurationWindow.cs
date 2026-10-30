using System.Text.Json.Nodes;
using System.Windows;
using System.Windows.Controls;
using Microsoft.Win32;

namespace Tongxu.Desktop;

public sealed class ConfigurationWindow : Window
{
    public JsonObject? Request
    {
        get; private set;
    }
    public JsonNode? SavedReply
    {
        get; private set;
    }
    public ConfigurationWindow(LocalClient client, JsonNode settings, bool paths)
    {
        Title = paths ? Localization.T("编辑服务目录") : Localization.T("连接配置");
        Width = 780;
        Height = 680;
        MinWidth = 620;
        MinHeight = 460;
        WindowStartupLocation = WindowStartupLocation.CenterOwner;
        var layout = new Grid { Margin = new Thickness(28) };
        layout.RowDefinitions.Add(new()
        {
            Height = new GridLength(1, GridUnitType.Star)
        });
        layout.RowDefinitions.Add(new()
        {
            Height = GridLength.Auto
        });
        Content = layout;
        var form = new StackPanel();
        layout.Children.Add(new ScrollViewer { Content = form, VerticalScrollBarVisibility = ScrollBarVisibility.Auto });
        form.Children.Add(new TextBlock { Text = Title, FontSize = 24, FontWeight = FontWeights.SemiBold, Margin = new Thickness(0, 0, 0, 16) });
        var inputs = new Dictionary<string, TextBox>();
        TextBox Add(string label, string value = "")
        {
            form.Children.Add(new TextBlock { Text = label, Margin = new Thickness(0, 14, 0, 7) });
            var t = new TextBox { Text = value };
            form.Children.Add(t);
            return t;
        }
        TextBox? bot = null, owner = null, first = null;
        PasswordBox? secret = null;
        if (paths)
        {
            form.Children.Add(new TextBlock { Text = Localization.T("保存后下次启动生效。不搬迁、复制或删除原目录中的资料；所有路径由现有后端校验。"), TextWrapping = TextWrapping.Wrap });
            foreach (var (key, label) in new[] { ("DataDir", Localization.T("文档目录")), ("WorkDir", Localization.T("任务临时目录")), ("SoftwareDir", Localization.T("用户依赖目录")), ("StateDir", Localization.T("会话状态目录")), ("CodexHome", Localization.T("专用 Codex 登录目录")), ("CodexCommand", Localization.T("Codex CLI 程序")) })
            {
                var input = Add(label, settings["Paths"].S(key));
                inputs[key] = input;
                var choose = new Button { Content = Localization.T("选择…"), HorizontalAlignment = HorizontalAlignment.Left, Margin = new Thickness(0, 6, 0, 0) };
                choose.Click += (_, _) => { if (key == "CodexCommand") { var p = new OpenFileDialog { Filter = Localization.T("原生程序|*.exe") }; if (p.ShowDialog(this) == true) input.Text = p.FileName; } else { var p = new OpenFolderDialog { Title = label }; if (p.ShowDialog(this) == true) input.Text = p.FolderName; } };
                form.Children.Add(choose);
            }
        }
        else
        {
            foreach (var (label, id, value) in new[] { ("消息平台", "wecom", "企业微信"), ("Agent", "codex", "Codex CLI") })
            {
                form.Children.Add(new TextBlock { Text = Localization.T(label), Margin = new Thickness(0, 14, 0, 7) });
                form.Children.Add(MainWindow.AdapterChoice(id, value));
            }
            form.Children.Add(new TextBlock { Text = Localization.F($"管理员：{settings.S("Owner")}\n已保存机器人 ID：{settings.S("BotIDMasked")}\n现有凭据不会读取到普通界面。以下字段留空表示保留。"), TextWrapping = TextWrapping.Wrap });
            bot = Add(Localization.T("新机器人 ID（留空保持）"));
            form.Children.Add(new TextBlock { Text = Localization.T("新机器人 Secret（留空保持）"), Margin = new Thickness(0, 16, 0, 8) });
            secret = new PasswordBox();
            form.Children.Add(secret);
            if (settings.S("Version") == "")
            {
                owner = Add(Localization.T("首次配置的机主稳定用户 ID"), settings.S("Owner"));
                first = Add(Localization.T("首次配置的群 ID"));
            }
        }
        var feedback = new TextBlock { TextWrapping = TextWrapping.Wrap, Margin = new Thickness(0, 14, 0, 0) };
        form.Children.Add(feedback);
        var actions = new StackPanel { Orientation = Orientation.Horizontal, HorizontalAlignment = HorizontalAlignment.Right, Margin = new Thickness(0, 18, 0, 0) };
        var cancel = new Button { Content = Localization.T("取消"), IsCancel = true };
        var review = new Button { Content = Localization.T("重新核对当前设置") };
        var save = new Button { Content = Localization.T("验证并保存（下次启动生效）"), Style = (Style)Application.Current.FindResource("Primary") };
        actions.Children.Add(review);
        actions.Children.Add(cancel);
        actions.Children.Add(save);
        Grid.SetRow(actions, 1);
        layout.Children.Add(actions);
        var expected = settings.S("Version");
        bool saving = false;
        Closing += (_, e) => { if (saving) e.Cancel = true; };
        review.Click += async (_, _) => { try { var current = await client.SettingsAsync("read"); if (!current.B("Success")) throw new InvalidOperationException(current.S("Message")); var summary = Localization.F($"管理员：{current.S("Owner")}\n已保存机器人 ID：{current.S("BotIDMasked")}\n") + string.Join("\n", current["Paths"]!.AsObject().Select(p => p.Key + ": " + p.Value)) + Localization.T("\n\n现有群与成员名单将保留。是否采用这份设置作为本次保存基准？表单输入不会自动覆盖。"); if (MessageBox.Show(this, summary, Localization.T("核对当前设置"), MessageBoxButton.OKCancel) == MessageBoxResult.OK) { expected = current.S("Version"); feedback.Text = Localization.T("已采用刚刚核对的设置版本，表单输入已保留。"); } } catch (Exception ex) { feedback.Text = SafeText.Clean(ex.Message); } };
        save.Click += async (_, _) =>
        {
            if (saving)
                return;
            if (inputs.Any(p => string.IsNullOrWhiteSpace(p.Value.Text)))
            {
                feedback.Text = Localization.T("路径不能为空。");
                return;
            }
            Request = new JsonObject { ["Version"] = expected };
            if (paths)
            {
                var values = new JsonObject();
                foreach (var p in inputs)
                    values[p.Key] = p.Value.Text.Trim();
                Request["Paths"] = values;
            }
            else
            {
                Request["BotID"] = bot!.Text.Trim();
                Request["Secret"] = secret!.Password;
                if (owner != null)
                {
                    Request["Owner"] = owner.Text.Trim();
                    Request["FirstGroup"] = first!.Text.Trim();
                }
            }
            saving = true;
            save.IsEnabled = false;
            cancel.IsEnabled = false;
            review.IsEnabled = false;
            feedback.Text = Localization.T("正在校验并保存，请等待结果确认…");
            try
            {
                var reply = await client.SettingsAsync("save", Request);
                if (!reply.B("Success"))
                {
                    feedback.Text = reply.S("Message") + Localization.T(" 输入已保留；设置变化时请重新核对。");
                }
                else
                {
                    SavedReply = reply;
                    saving = false;
                    DialogResult = true;
                }
            }
            catch (Exception ex) { feedback.Text = SafeText.Clean(ex.Message) + Localization.T(" 输入已保留，请核对后再操作。"); }
            finally { saving = false; save.IsEnabled = true; cancel.IsEnabled = true; review.IsEnabled = true; }
        };
        WindowCaption.Attach(this, layout);
    }
}
