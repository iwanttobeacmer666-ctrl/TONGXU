param(
    [string]$ExecutablePath = (Join-Path $PSScriptRoot 'bin\desktop\tongxu.exe')
)
$ErrorActionPreference = 'Stop'
function Get-TongxuShortcutName([uint16]$WindowsUILanguage) {
    # Chinese includes Simplified, Traditional, Hong Kong, Singapore and Macao.
    if (($WindowsUILanguage -band 0x03ff) -eq 0x0004) { return '同序' }
    return 'TONGXU'
}
function Test-TongxuShortcutTarget($Shell, [string]$Path, [string]$Executable) {
    $taskExisting = $null
    try {
        $taskExisting = $Shell.CreateShortcut($Path)
        if (-not $taskExisting.TargetPath -or $taskExisting.Arguments) { return $false }
        return [IO.Path]::GetFullPath($taskExisting.TargetPath) -ieq $Executable
    } catch { return $false }
    finally {
        if ($null -ne $taskExisting) { [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($taskExisting) }
    }
}
if (-not ('Tongxu.ShortcutWindowsLanguage' -as [type])) {
    Add-Type -TypeDefinition @'
using System.Runtime.InteropServices;
namespace Tongxu {
    public static class ShortcutWindowsLanguage {
        [DllImport("kernel32.dll")]
        public static extern ushort GetUserDefaultUILanguage();
    }
}
'@
}
$taskWindowsUILanguage = [Tongxu.ShortcutWindowsLanguage]::GetUserDefaultUILanguage()
$taskShortcutName = Get-TongxuShortcutName $taskWindowsUILanguage
$taskExecutable = [IO.Path]::GetFullPath($ExecutablePath)
if ([IO.Path]::GetExtension($taskExecutable) -ine '.exe' -or -not (Test-Path -LiteralPath $taskExecutable -PathType Leaf)) {
    throw 'The tongxu executable was not found. Build or install it first.'
}
$taskDesktop = [Environment]::GetFolderPath([Environment+SpecialFolder]::DesktopDirectory)
if (-not $taskDesktop -or -not (Test-Path -LiteralPath $taskDesktop -PathType Container)) {
    throw 'The current user desktop is unavailable.'
}
$taskShortcutPath = Join-Path $taskDesktop ($taskShortcutName + '.lnk')
$taskRenamedFrom = $null
$taskShell = New-Object -ComObject WScript.Shell
$taskShortcut = $null
try {
    if (Test-Path -LiteralPath $taskShortcutPath) {
        if (-not (Test-TongxuShortcutTarget $taskShell $taskShortcutPath $taskExecutable)) {
            throw 'An unrelated shortcut already uses this name; it was not replaced.'
        }
    } else {
        # Migrate only this application's link, including the earlier combined name.
        # An unrelated link is neither renamed nor overwritten.
        foreach ($taskOldName in @('同序 tongxu', '同序', 'TONGXU')) {
            $taskOldPath = Join-Path $taskDesktop ($taskOldName + '.lnk')
            if ($taskOldPath -ine $taskShortcutPath -and (Test-Path -LiteralPath $taskOldPath -PathType Leaf) -and (Test-TongxuShortcutTarget $taskShell $taskOldPath $taskExecutable)) {
                if ([IO.Path]::GetDirectoryName([IO.Path]::GetFullPath($taskOldPath)) -ine [IO.Path]::GetDirectoryName([IO.Path]::GetFullPath($taskShortcutPath))) {
                    throw 'Shortcut rename must stay on the current user desktop.'
                }
                Move-Item -LiteralPath $taskOldPath -Destination $taskShortcutPath
                $taskRenamedFrom = $taskOldPath
                break
            }
        }
    }
    $taskShortcut = $taskShell.CreateShortcut($taskShortcutPath)
    $taskShortcut.TargetPath = $taskExecutable
    $taskShortcut.Arguments = ''
    $taskShortcut.WorkingDirectory = [IO.Path]::GetDirectoryName($taskExecutable)
    $taskShortcut.Description = if ($taskShortcutName -eq '同序') { '同序 · 工作台' } else { 'TONGXU Workspace' }
    # Use the embedded icon so deployment never depends on a preview/temporary file.
    $taskShortcut.IconLocation = $taskExecutable + ',0'
    $taskShortcut.WindowStyle = 1
    $taskShortcut.Save()
    [pscustomobject]@{
        Shortcut = $taskShortcutPath
        Target = $taskShortcut.TargetPath
        Icon = $taskShortcut.IconLocation
        WorkingDirectory = $taskShortcut.WorkingDirectory
        Name = $taskShortcutName
        WindowsUILanguage = $taskWindowsUILanguage
        RenamedFrom = $taskRenamedFrom
    }
} finally {
    if ($null -ne $taskShortcut) { [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($taskShortcut) }
    [void][Runtime.InteropServices.Marshal]::FinalReleaseComObject($taskShell)
}
