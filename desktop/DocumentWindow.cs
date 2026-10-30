using System.IO;
using System.Text;
using System.Text.Json.Nodes;
using System.Windows;
using System.Windows.Controls;
using Microsoft.Win32;

namespace Tongxu.Desktop;

public sealed class DocumentWindow : Window
{
    readonly LocalClient _client; readonly string _group, _user, _name;
    readonly TextBox _text = new() { IsReadOnly = true, AcceptsReturn = true, VerticalScrollBarVisibility = ScrollBarVisibility.Auto, HorizontalScrollBarVisibility = ScrollBarVisibility.Auto, TextWrapping = TextWrapping.NoWrap }; readonly TextBlock _status = new(); readonly Button _more, _save;
    readonly Decoder _decoder = new UTF8Encoding(false, true).GetDecoder(); string _version = ""; long _offset, _total; bool _binary, _busy, _closed, _readFailed;
    public DocumentWindow(LocalClient client, string group, string user, string name)
    {
        _client = client;
        _group = group;
        _user = user;
        _name = name;
        Title = Localization.T("读取 · ") + name;
        Width = 1000;
        Height = 720;
        MinWidth = 660;
        MinHeight = 460;
        WindowStartupLocation = WindowStartupLocation.CenterOwner;
        var grid = new Grid { Margin = new Thickness(26) };
        foreach (var h in new[] { GridLength.Auto, new GridLength(1, GridUnitType.Star), GridLength.Auto })
            grid.RowDefinitions.Add(new()
            {
                Height = h
            });
        Content = grid;
        grid.Children.Add(new TextBlock { Text = Localization.F($"群：{group}  ·  {(user == "public" ? Localization.T("公共文件") : Localization.T("私人文件 · ") + user)}\n{name}"), Margin = new Thickness(0, 0, 0, 18) });
        Grid.SetRow(_text, 1);
        grid.Children.Add(_text);
        var actions = new WrapPanel { Margin = new Thickness(0, 16, 0, 0) };
        actions.Children.Add(_status);
        _more = new()
        {
            Content = Localization.T("继续读取下一块")
        };
        _save = new()
        {
            Content = Localization.T("保存完整副本")
        };
        _more.Click += async (_, _) => await ReadMoreAsync();
        _save.Click += async (_, _) => await SaveAsync();
        actions.Children.Add(_more);
        actions.Children.Add(_save);
        Grid.SetRow(actions, 2);
        grid.Children.Add(actions);
        Loaded += async (_, _) => await ReadMoreAsync();
        Closed += (_, _) => _closed = true;
        WindowCaption.Attach(this, grid);
    }
    JsonObject Request(long offset) => new() { ["Action"] = "document-read", ["Group"] = _group, ["User"] = _user, ["Name"] = _name, ["Offset"] = offset, ["FileVersion"] = _version };
    static JsonNode Check(JsonNode reply) => reply.B("Success") ? reply["Documents"] ?? throw new InvalidOperationException(Localization.T("缺少文件响应。")) : throw new InvalidOperationException(reply.S("Message"));
    void Busy(bool busy)
    {
        _busy = busy;
        _save.IsEnabled = !busy;
        _more.IsEnabled = !busy && !_binary && !_readFailed && (_offset < _total || _version == "") && _offset < 2 << 20;
    }
    internal async Task ReadMoreAsync()
    {
        if (_busy || _closed)
            return;
        Busy(true);
        try
        {
            var data = Check(await _client.SendAsync(Request(_offset)));
            var bytes = Convert.FromBase64String(data.S("Data"));
            _version = data.S("SHA256");
            _total = data.N("Total");
            var next = data.N("NextOffset");
            if (next < _offset || next > _total || next - _offset != bytes.Length)
                throw new InvalidOperationException(Localization.T("读取偏移不一致，请重新打开文件。"));
            try
            {
                if (bytes.Contains((byte)0))
                    throw new DecoderFallbackException();
                var chars = new char[Encoding.UTF8.GetMaxCharCount(bytes.Length)];
                var count = _decoder.GetChars(bytes, 0, bytes.Length, chars, 0, next == _total);
                _text.AppendText(new string(chars, 0, count));
            }
            catch (DecoderFallbackException) { _binary = true; _text.Text = Localization.T("此文件不是可直接预览的 UTF-8 文本。请保存完整副本，再使用本机文档应用打开。副本按后端文件版本分块校验，不改变原文件。"); }
            _offset = next;
            _status.Text = Localization.F($"已读取 {J.Bytes(_offset)} / {J.Bytes(_total)}  ") + (_offset >= 2 << 20 && _offset < _total ? Localization.T("已达文字预览上限，请保存完整副本。") : "");
        }
        catch (Exception ex) { _readFailed = true; _status.Text = SafeText.Clean(ex.Message) + Localization.T(" 请关闭后刷新并重新打开文件。"); }
        finally { if (!_closed) Busy(false); }
    }
    async Task SaveAsync()
    {
        if (_busy)
            return;
        var dialog = new SaveFileDialog { FileName = Path.GetFileName(_name), OverwritePrompt = true };
        if (dialog.ShowDialog(this) != true)
            return;
        Busy(true);
        string? temp = null;
        try
        {
            temp = Path.Combine(Path.GetDirectoryName(dialog.FileName)!, ".group-assistant-copy-" + Guid.NewGuid().ToString("N"));
            await using (var stream = new FileStream(temp, FileMode.CreateNew, FileAccess.Write, FileShare.None))
            {
                long offset = 0;
                do
                {
                    var data = Check(await _client.SendAsync(Request(offset)));
                    if (_version == "")
                        _version = data.S("SHA256");
                    var bytes = Convert.FromBase64String(data.S("Data"));
                    var next = data.N("NextOffset");
                    if (next < offset || next - offset != bytes.Length || next > data.N("Total") || next == offset && next < data.N("Total"))
                        throw new InvalidOperationException(Localization.T("文件响应不完整，未保存副本。"));
                    await stream.WriteAsync(bytes);
                    offset = next;
                    _status.Text = Localization.F($"保存副本：{J.Bytes(offset)} / {J.Bytes(data.N("Total"))}");
                    if (offset >= data.N("Total"))
                        break;
                } while (!_closed);
                if (_closed)
                    throw new OperationCanceledException();
                await stream.FlushAsync();
            }
            File.Move(temp, dialog.FileName, true);
            temp = null;
            _status.Text = Localization.T("完整副本已保存，原文件保持不变。");
        }
        catch (Exception ex) { _status.Text = SafeText.Clean(ex.Message); }
        finally { if (temp != null && File.Exists(temp)) File.Delete(temp); if (!_closed) Busy(false); }
    }
}
