using System.IO;
using System.Text.Json.Nodes;
using System.Windows;
using System.Windows.Media;
using System.Windows.Media.Imaging;
using System.Windows.Threading;

namespace Tongxu.Desktop;

internal static class DesktopVerification
{
    public static async Task RunAsync(LocalClient client, string? evidence)
    {
        if (!client.Root.Contains("TestDesktopFrontendIsolatedIntegration", StringComparison.OrdinalIgnoreCase) || !File.Exists(Path.Combine(client.Root, ".desktop-test-fixture")) || File.ReadAllText(Path.Combine(client.Root, ".desktop-test-fixture")) != "group-assistant-isolated-fixture-v1")
            throw new InvalidOperationException("Self-test refuses a non-isolated project.");
        var settings = await client.SettingsAsync("read");
        Assert(settings.B("Success") && settings.S("Owner") == "desktop-fixture-owner", "isolated settings read");
        var checks = new List<string>();
        string version = "";
        async Task<JsonNode> Read(JsonObject q)
        {
            var r = await client.SendAsync(q);
            Assert(r.B("Success"), q.S("Action") + ": " + r.S("Message"));
            if (r.S("Version") != "")
                version = r.S("Version");
            return r;
        }
        async Task<JsonNode> Write(JsonObject q)
        {
            q["Version"] = version;
            return await Read(q);
        }
        var roster = await Read(new()
        {
            ["Action"] = "list"
        });
        Assert(roster.S("Owner") == "desktop-fixture-owner", "owner projection");
        var window = new MainWindow(client) { IsVerification = true };
        await window.InitializeAsync();
        IconVerification.CheckPackaged(window);
        evidence ??= Path.Combine(client.Root, "evidence");
        Directory.CreateDirectory(evidence);
        for (int i = 0; i < 6; i++)
        {
            await window.NavigateAsync(i);
            await Dispatcher.Yield(DispatcherPriority.ContextIdle);
            Capture(window, Path.Combine(evidence, $"desktop-{i + 1}-wide.png"), 1360, 860);
            Capture(window, Path.Combine(evidence, $"desktop-{i + 1}-compact.png"), 1040, 740);
            Capture(window, Path.Combine(evidence, $"desktop-{i + 1}-small.png"), 960, 640);
        }
        checks.Add("Six real-backend pages rendered at 1360x860, 1040x740 and 960x640 with long IDs.");
        checks.Add("Ten embedded icon sizes, transparent corners and shared application/window image verified.");
        await window.VerifyViewStateAsync(evidence);
        checks.Add("1000+ member rows stay virtualized; search, selection and long-output scroll survive polling.");
        await window.VerifyPreferencesAsync(evidence);
        checks.Add("Background image persistence/reset uses isolated desktop settings.");
        await window.VerifyLanguageAndCaptionAsync(evidence);
        checks.Add("Ten complete languages switch live and persist; headings, status labels and caption update without changing filters, selection, output or user content.");
        checks.Add("Failed preference save preserves language and settings; malformed appearance JSON is tolerated. Native caption minimize/maximize/restore/close and seamless caption canvas verified.");
        await window.VerifyThemeAndTrayAsync(evidence);
        checks.Add("Dark/light/system themes render and persist; failed save preserves UI; synthetic system transitions update shared palette. Online close registers a real shell icon, hides and restores the window. Single-option platform/Agent dropdowns verified.");
        foreach (var pathsDialog in new[] { false, true })
        {
            var configuration = new ConfigurationWindow(client, settings, pathsDialog);
            CaptureElement((FrameworkElement)configuration.Content, Path.Combine(evidence, pathsDialog ? "desktop-path-dialog.png" : "desktop-connection-dialog.png"), 724, 590);
            configuration.Close();
        }
        var reader = new DocumentWindow(client, "fixture-group", "public", "large.txt");
        await reader.ReadMoreAsync();
        await reader.ReadMoreAsync();
        CaptureElement((FrameworkElement)reader.Content, Path.Combine(evidence, "desktop-document-reader.png"), 948, 650);
        reader.Close();
        var busy = await client.SendAsync(new()
        {
            ["Action"] = "temporary-clear-preview",
            ["Version"] = version
        });
        Assert(!busy.B("Success") && busy.S("Code") == "busy", "busy temporary clear refused");
        var tasks = await Read(new()
        {
            ["Action"] = "tasks"
        });
        var active = tasks.Rows("TaskItems").First(n => n.B("CanStop"));
        var detail = await Read(new()
        {
            ["Action"] = "task-detail",
            ["Task"] = active.S("ID")
        });
        var serialized = detail.ToJsonString();
        Assert(!serialized.Contains("PRIVATE_REASONING") && !serialized.Contains("FAKE_SECRET_VALUE"), "reasoning and credentials excluded");
        await Read(new()
        {
            ["Action"] = "stop",
            ["Task"] = active.S("ID")
        });
        checks.Add("All-group task detail, redaction and confirmed stop through actual broker.");
        async Task<JsonNode> Review(JsonNode p)
        {
            while (!p.B("ReviewComplete"))
            {
                var q = new JsonObject { ["Action"] = "file-operation", ["Version"] = p.S("Version"), ["Confirm"] = p.S("Token"), ["Scope"] = p.S("Kind"), ["Offset"] = p.N("NextOffset"), ["Limit"] = 100 };
                if (p.S("Kind") != "temporary")
                {
                    q["Group"] = p.S("Group");
                    q["User"] = p.S("User");
                }
                p = (await Read(q))["FileOperation"]!;
            }
            return p;
        }
        var tmp = (await Write(new()
        {
            ["Action"] = "temporary-clear-preview",
            ["Limit"] = 1
        }))["FileOperation"]!;
        var manifest = new FileReviewWindow(client, tmp, "清空全局临时区");
        Assert(!manifest.ReadyToConfirm, "confirmation disabled before full manifest delivery");
        await manifest.LoadAsync();
        Assert(manifest.ReadyToConfirm, "complete manifest unlocks confirmation");
        manifest.ShowInTaskbar = false;
        manifest.WindowStartupLocation = WindowStartupLocation.Manual;
        manifest.Left = -10000;
        manifest.Top = -10000;
        manifest.Show();
        await Dispatcher.Yield(DispatcherPriority.ContextIdle);
        CaptureElement(manifest, Path.Combine(evidence, "desktop-manifest-dialog.png"), (int)manifest.ActualWidth, (int)manifest.ActualHeight);
        string VisualText(DependencyObject node)
        {
            var value = node is System.Windows.Controls.TextBlock text ? text.Text : "";
            for (int i = 0; i < VisualTreeHelper.GetChildrenCount(node); i++)
                value += "\n" + VisualText(VisualTreeHelper.GetChild(node, i));
            return value;
        }
        Assert(VisualText((FrameworkElement)manifest.Content).Contains("remnant.txt"), "manifest filenames are rendered for review");
        manifest.Close();
        var cleared = await Write(new()
        {
            ["Action"] = "temporary-clear",
            ["Confirm"] = tmp.S("Token")
        });
        Assert(cleared["FileOperation"].B("Complete"), "temporary completion");
        checks.Add("Actual manifest dialog pages full inventory before enabling confirmation; idle clear completes.");
        await Write(new()
        {
            ["Action"] = "enable",
            ["Group"] = "fixture-group",
            ["User"] = "bob"
        });
        var before = await Read(new()
        {
            ["Action"] = "document-read",
            ["Group"] = "fixture-group",
            ["User"] = "bob",
            ["Name"] = "bob.txt"
        });
        Assert(Convert.FromBase64String(before["Documents"].S("Data")).Length > 0, "reenable retains private document");
        await Write(new()
        {
            ["Action"] = "disable",
            ["Group"] = "fixture-group",
            ["User"] = "bob"
        });
        var member = (await Write(new()
        {
            ["Action"] = "delete-preview",
            ["Group"] = "fixture-group",
            ["User"] = "bob"
        }))["DeletePreview"]!;
        await Write(new()
        {
            ["Action"] = "delete",
            ["Group"] = "fixture-group",
            ["User"] = "bob",
            ["Confirm"] = member.S("Token")
        });
        checks.Add("Reenable preserves data; disable and scoped member preview/confirm deletion.");
        var first = (await Read(new()
        {
            ["Action"] = "document-read",
            ["Group"] = "fixture-group",
            ["User"] = "public",
            ["Name"] = "large.txt"
        }))["Documents"]!;
        Assert(Convert.FromBase64String(first.S("Data")).Length == 65536, "chunk size");
        await Read(new()
        {
            ["Action"] = "document-read",
            ["Group"] = "fixture-group",
            ["User"] = "public",
            ["Name"] = "large.txt",
            ["Offset"] = first.N("NextOffset"),
            ["FileVersion"] = first.S("SHA256")
        });
        var stale = (await Write(new()
        {
            ["Action"] = "documents-clear-preview",
            ["Group"] = "fixture-group",
            ["User"] = "public"
        }))["FileOperation"]!;
        await Write(new()
        {
            ["Action"] = "group-add",
            ["Group"] = "fixture-added"
        });
        var conflict = await client.SendAsync(new()
        {
            ["Action"] = "documents-clear",
            ["Version"] = stale.S("Version"),
            ["Group"] = "fixture-group",
            ["User"] = "public",
            ["Confirm"] = stale.S("Token")
        });
        Assert(!conflict.B("Success") && conflict.S("Code") == "version_conflict", "stale preview version");
        var selected = (await Write(new()
        {
            ["Action"] = "documents-delete-preview",
            ["Group"] = "fixture-group",
            ["User"] = "public",
            ["Names"] = new JsonArray("public-a.txt")
        }))["FileOperation"]!;
        var deleted = await Write(new()
        {
            ["Action"] = "documents-delete",
            ["Group"] = "fixture-group",
            ["User"] = "public",
            ["Confirm"] = selected.S("Token")
        });
        Assert(deleted["FileOperation"].B("Complete"), "selected delete completion");
        var area = await Review((await Write(new()
        {
            ["Action"] = "documents-clear-preview",
            ["Group"] = "fixture-group",
            ["User"] = "public",
            ["Limit"] = 1
        }))["FileOperation"]!);
        var areaResult = await Write(new()
        {
            ["Action"] = "documents-clear",
            ["Group"] = "fixture-group",
            ["User"] = "public",
            ["Confirm"] = area.S("Token")
        });
        Assert(areaResult["FileOperation"].B("Complete"), "complete public area clear");
        await Write(new()
        {
            ["Action"] = "group-disable",
            ["Group"] = "fixture-added"
        });
        await Write(new()
        {
            ["Action"] = "group-enable",
            ["Group"] = "fixture-added"
        });
        await Write(new()
        {
            ["Action"] = "cleanup-retry",
            ["Group"] = "fixture-group",
            ["User"] = "carol"
        });
        checks.Add("Versioned chunks, selected/full-area deletion, group toggles, cleanup recovery and stale-configuration refusal.");
        var invalid = await client.SettingsAsync("save", new()
        {
            ["Version"] = "old",
            ["BotID"] = "",
            ["Secret"] = ""
        });
        Assert(!invalid.B("Success"), "configuration version conflict");
        checks.Add("Settings adapter refuses stale writes.");
        Assert(await window.ExitFromTrayAsync(), "tray exit completed service shutdown");
        bool offline = false;
        for (int i = 0; i < 30; i++)
        {
            await Task.Delay(200);
            try
            {
                var r = await client.SendAsync(new()
                {
                    ["Action"] = "status"
                });
                if (r.B("Success") && !r.B("Online"))
                {
                    offline = true;
                    break;
                }
            }
            catch { }
        }
        Assert(offline, "graceful service exit observed");
        await window.RefreshAsync(true);
        await window.NavigateAsync(4);
        Capture(window, Path.Combine(evidence, "desktop-offline.png"), 1040, 740);
        await window.VerifyOfflineCloseAsync();
        checks.Add("Tray exit invokes graceful service-stop and observes offline; offline window close fully closes without a tray icon.");
        var latest = await client.SettingsAsync("read");
        var paths = latest["Paths"]!.DeepClone();
        var nextWork = Path.Combine(client.Root, "WORK", "tmp-next");
        paths["WorkDir"] = nextWork;
        var saved = await client.SettingsAsync("save", new()
        {
            ["Version"] = latest.S("Version"),
            ["BotID"] = "fixture-updated",
            ["Secret"] = "",
            ["Paths"] = paths
        });
        Assert(saved.B("Success"), "validated offline settings save: " + saved.ToJsonString());
        Assert(!Directory.Exists(nextWork), "settings save must not create/migrate work folders");
        var readback = await client.SettingsAsync("read");
        Assert(readback["Paths"].S("WorkDir") == nextWork && !readback.ToJsonString().Contains("NEVER-DECRYPTED"), "masked settings/path persistence");
        checks.Add("Existing locked/validated settings save and path persistence, without migration or credential exposure.");
        await File.WriteAllTextAsync(Path.Combine(evidence, "verification.json"), new JsonObject { ["Success"] = true, ["VerifiedAtUtc"] = DateTimeOffset.UtcNow.ToString("O"), ["Checks"] = new JsonArray(checks.Select(s => JsonValue.Create(s)).ToArray()), ["RealWeComConnected"] = false, ["RealModelCalled"] = false }.ToJsonString());
        var failure = Path.Combine(evidence, "failure.txt");
        if (File.Exists(failure))
            File.Delete(failure);
        window.Close();
    }
    internal static void Capture(MainWindow window, string path, int width, int height)
    {
        CaptureElement(window.Surface, path, width, height);
    }
    internal static void CaptureElement(FrameworkElement surface, string path, int width, int height)
    {
        surface.Measure(new Size(width, height));
        surface.Arrange(new Rect(0, 0, width, height));
        surface.UpdateLayout();
        var image = new RenderTargetBitmap(width, height, 96, 96, PixelFormats.Pbgra32);
        var backdrop = new DrawingVisual();
        using (var drawing = backdrop.RenderOpen())
            drawing.DrawRectangle(System.Windows.Media.Brushes.White, null, new Rect(0, 0, width, height));
        image.Render(backdrop);
        image.Render(surface);
        var png = new PngBitmapEncoder();
        png.Frames.Add(BitmapFrame.Create(image));
        using var stream = File.Create(path);
        png.Save(stream);
    }
    static void Assert(bool value, string message)
    {
        if (!value)
            throw new InvalidOperationException("Verification failed: " + message);
    }
}

public partial class MainWindow
{
    internal async Task VerifyPreferencesAsync(string evidence)
    {
        _background = Path.Combine(evidence, "desktop-1-wide.png");
        ApplyBackground(_background);
        await SavePreferencesAsync();
        _background = "";
        LoadPreferences();
        if (_background == "" || Surface.Background is not ImageBrush)
            throw new InvalidOperationException("Background preference persistence failed.");
        _background = "";
        Surface.Background = Theme.Get("Surface");
        await SavePreferencesAsync();
        LoadPreferences();
        if (_background != "")
            throw new InvalidOperationException("Background reset failed.");
    }
    internal async Task VerifyViewStateAsync(string evidence)
    {
        await NavigateAsync(1);
        DesktopVerification.Capture(this, Path.Combine(evidence, "desktop-members-many.png"), 1040, 740);
        if (_members.Count < 1000)
            throw new InvalidOperationException("Large-list fixture missing.");
        int CountRows(System.Windows.DependencyObject parent)
        {
            int count = parent is System.Windows.Controls.DataGridRow ? 1 : 0;
            for (int i = 0; i < System.Windows.Media.VisualTreeHelper.GetChildrenCount(parent); i++)
                count += CountRows(System.Windows.Media.VisualTreeHelper.GetChild(parent, i));
            return count;
        }
        if (CountRows(_memberTable) > 100)
            throw new InvalidOperationException("Member virtualization failed.");
        _memberSearch.Text = "long-user-id";
        if (_memberTable.Items.Count != 1)
            throw new InvalidOperationException("Member search failed.");
        _memberTable.SelectedItem = _memberTable.Items[0];
        var selected = _memberTable.SelectedItem;
        await RefreshAsync(false);
        if (_memberSearch.Text != "long-user-id" || !ReferenceEquals(selected, _memberTable.SelectedItem))
            throw new InvalidOperationException("Member selection/search reset by polling.");
        DesktopVerification.Capture(this, Path.Combine(evidence, "desktop-member-search.png"), 1040, 740);
        _memberSearch.Text = "";
        await NavigateAsync(3);
        DesktopVerification.Capture(this, Path.Combine(evidence, "desktop-task-long-output.png"), 1040, 740);
        _eventOutput.ScrollToVerticalOffset(200);
        Surface.UpdateLayout();
        var offset = _eventOutput.VerticalOffset;
        var output = _eventOutput.Text;
        await RefreshAsync(false);
        if (output != _eventOutput.Text || Math.Abs(offset - _eventOutput.VerticalOffset) > 1)
            throw new InvalidOperationException("Task output duplicated or scroll reset by polling.");
        var ids = System.Text.RegularExpressions.Regex.Matches(_eventOutput.Text, @"(?m)^#(\d+)").Select(m => m.Groups[1].Value).ToList();
        if (ids.Count == 0 || ids.Distinct().Count() != ids.Count)
            throw new InvalidOperationException("Task event sequences missing/duplicated.");
        var settingsPath = Client.OperatorSettings;
        var settingsBytes = await File.ReadAllBytesAsync(settingsPath);
        try
        {
            await File.WriteAllTextAsync(settingsPath, "{}");
            await RefreshAsync(false);
            if (_serviceKnown || _startButton.IsEnabled || _stopButton.IsEnabled || _serviceState.Text != "暂不可确认")
                throw new InvalidOperationException("Unavailable state misreported as confirmed offline.");
            DesktopVerification.Capture(this, Path.Combine(evidence, "desktop-uncertain.png"), 1040, 740);
        }
        finally { await File.WriteAllBytesAsync(settingsPath, settingsBytes); }
        await RefreshAsync(false);
    }
}
