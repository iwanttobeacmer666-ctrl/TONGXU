using System.ComponentModel;
using System.Globalization;
using System.IO;
using System.Reflection;
using System.Text.Json;
using System.Windows;
using System.Windows.Data;
using System.Windows.Markup;
using System.Windows.Media;

namespace Tongxu.Desktop;

// Only interface messages enter this catalogue. IDs, paths, documents and
// execution output are kept separate and are never passed through a tree scan.
internal static class Localization
{
    internal static readonly Choice[] Languages = [
        new("zh-CN", "简体中文"), new("zh-TW", "繁體中文"), new("en", "English"),
        new("ja", "日本語"), new("ko", "한국어"), new("es", "Español"),
        new("fr", "Français"), new("de", "Deutsch"), new("pt-BR", "Português (Brasil)"), new("ru", "Русский")
    ];
    static readonly Dictionary<string, Dictionary<string, string>> Catalogue = LoadCatalogue();
    static readonly List<WeakReference<Label>> Labels = [];
    public static string Current { get; private set; } = "zh-CN";
    internal static IReadOnlyDictionary<string, Dictionary<string, string>> Messages => Catalogue;

    static Dictionary<string, Dictionary<string, string>> LoadCatalogue()
    {
        using var stream = Assembly.GetExecutingAssembly().GetManifestResourceStream("Tongxu.Desktop.Localization.Strings.json") ?? throw new InvalidOperationException("Interface translations are unavailable.");
        return JsonSerializer.Deserialize<Dictionary<string, Dictionary<string, string>>>(stream) ?? throw new InvalidOperationException("Invalid interface translations.");
    }
    public static string T(string source) => Catalogue[Current].TryGetValue(source, out var value) ? value : source;
    public static string F(FormattableString message) => string.Format(CultureInfo.GetCultureInfo(Current), T(message.Format), message.GetArguments());
    public static void Bind(DependencyObject target, DependencyProperty property, string source)
    {
        var label = new Label(source);
        Labels.Add(new(label));
        BindingOperations.SetBinding(target, property, new Binding(nameof(Label.Value)) { Source = label, Mode = BindingMode.OneWay });
    }
    public static void SetLanguage(string code)
    {
        if (!Catalogue.ContainsKey(code))
            throw new ArgumentException("Unsupported interface language.", nameof(code));
        Current = code;
        for (int i = Labels.Count - 1; i >= 0; i--)
            if (Labels[i].TryGetTarget(out var label))
                label.Refresh();
            else
                Labels.RemoveAt(i);
    }
    public static void LoadPreference(string root)
    {
        var path = Path.Combine(AppPaths.Runtime(root), "desktop-settings.json");
        try
        {
            if (!File.Exists(path) || new FileInfo(path).Length > 16384)
                return;
            using var json = JsonDocument.Parse(File.ReadAllText(path));
            if (json.RootElement.ValueKind == JsonValueKind.Object && json.RootElement.TryGetProperty("Language", out var value) && value.ValueKind == JsonValueKind.String && Catalogue.ContainsKey(value.GetString()!))
                SetLanguage(value.GetString()!);
        }
        catch (IOException) { }
        catch (UnauthorizedAccessException) { }
        catch (JsonException) { }
    }
    public static void ApplyTo(Window window)
    {
        window.Language = XmlLanguage.GetLanguage(Current);
        window.FontFamily = new FontFamily(Current switch
        {
            "ja" => "Yu Gothic UI, Meiryo, Segoe UI",
            "ko" => "Malgun Gothic, Segoe UI",
            _ => "Segoe UI, Microsoft YaHei UI"
        });
    }
    sealed class Label(string source) : INotifyPropertyChanged
    {
        public string Value => T(source);
        public event PropertyChangedEventHandler? PropertyChanged;
        public void Refresh() => PropertyChanged?.Invoke(this, new(nameof(Value)));
    }
}
