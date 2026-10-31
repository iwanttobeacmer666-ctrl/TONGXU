using System.IO;
using System.Text.Json;

namespace Tongxu.Desktop;

internal static class AppPaths
{
    // Marker belongs to the future installer. Existing source deployments keep
    // their paths; merely upgrading the desktop never moves credentials.
    public static bool Installed(string root)
    {
        var marker = Path.Combine(root, "installed-layout.json");
        if (!File.Exists(marker)) return false;
        if (new FileInfo(marker).Length > 1024) throw new InvalidDataException("Invalid installed layout.");
        using var json = JsonDocument.Parse(File.ReadAllText(marker));
        if (json.RootElement.GetProperty("Version").GetInt32() != 1) throw new InvalidDataException("Unsupported installed layout.");
        return true;
    }
    public static string Runtime(string root) => Installed(root)
        ? Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData), "Tongxu", "runtime")
        : Path.Combine(root, "runtime");
}
