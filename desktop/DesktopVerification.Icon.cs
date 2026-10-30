using System.IO;
using System.Runtime.InteropServices;
using System.Windows;
using System.Windows.Interop;
using System.Windows.Media;
using System.Windows.Media.Imaging;

namespace Tongxu.Desktop;

internal static class IconVerification
{
    [DllImport("user32.dll")]
    static extern IntPtr SendMessage(IntPtr window, uint message, IntPtr parameter, IntPtr data);

    internal static void CheckPackaged(Window window)
    {
        if (window.Icon is not BitmapSource { PixelWidth: 256, PixelHeight: 256 })
            throw new InvalidOperationException("Application window icon is missing.");
        var resource = Application.GetResourceStream(new Uri("pack://application:,,,/tongxu;component/Assets/tongxu.ico"))
            ?? throw new InvalidOperationException("Packaged ICO missing.");
        using var stream = resource.Stream;
        var decoder = new IconBitmapDecoder(stream, BitmapCreateOptions.PreservePixelFormat, BitmapCacheOption.OnLoad);
        var expected = new[] { 16, 20, 24, 32, 40, 48, 64, 96, 128, 256 };
        if (!decoder.Frames.Select(frame => frame.PixelWidth).Order().SequenceEqual(expected))
            throw new InvalidOperationException("Windows icon resolutions incomplete.");
        foreach (var frame in decoder.Frames)
        {
            var pixels = new FormatConvertedBitmap(frame, PixelFormats.Bgra32, null, 0);
            var corner = new byte[4];
            pixels.CopyPixels(new Int32Rect(0, 0, 1, 1), corner, 4, 0);
            if (corner[3] > 1) throw new InvalidOperationException("Opaque black icon corner detected.");
        }
    }

    internal static void CheckNative(Window window)
    {
        var handle = new WindowInteropHelper(window).Handle;
        if (handle == IntPtr.Zero || SendMessage(handle, 0x007F, new IntPtr(1), IntPtr.Zero) == IntPtr.Zero ||
            SendMessage(handle, 0x007F, IntPtr.Zero, IntPtr.Zero) == IntPtr.Zero)
            throw new InvalidOperationException("Native taskbar/Alt-Tab icon handles missing.");
    }
}
