# Path discovery reads file metadata only. It never starts Codex or reads auth.
Set-StrictMode -Version Latest
$script:OperatorPathKeys = @('CodexCommand','DataDir','WorkDir','SoftwareDir','StateDir','CodexHome')

function Test-NativeCodexPath([string]$Path) {
    if (-not $Path -or $Path -notmatch '^[A-Za-z]:[\\/]' -or [IO.Path]::GetExtension($Path) -ine '.exe') { return $false }
    $stream = $null
    try {
        $null = Assert-LocalSetupPath (Split-Path $Path -Parent) -AllowDriveRoot
        if (-not (Test-Path -LiteralPath $Path -PathType Leaf) -or ((Get-Item -LiteralPath $Path -Force).Attributes -band [IO.FileAttributes]::ReparsePoint)) { return $false }
        $stream = [IO.File]::Open($Path,'Open','Read','ReadWrite')
        if ($stream.Length -lt 64 -or $stream.ReadByte() -ne 77 -or $stream.ReadByte() -ne 90) { return $false }
        $stream.Position = 60
        $offset = New-Object byte[] 4
        if ($stream.Read($offset,0,4) -ne 4) { return $false }
        $pe = [BitConverter]::ToUInt32($offset,0)
        if ($pe -gt $stream.Length - 4) { return $false }
        $stream.Position = $pe
        return $stream.ReadByte() -eq 80 -and $stream.ReadByte() -eq 69 -and $stream.ReadByte() -eq 0 -and $stream.ReadByte() -eq 0
    } catch { return $false }
    finally { if ($null -ne $stream) { $stream.Dispose() } }
}

function Find-NativeCodex([string]$Saved = '') {
    $paths = [Collections.Generic.List[string]]::new()
    if ($Saved) { $paths.Add($Saved) }
    $commands = @(Get-Command codex.exe,codex.cmd,codex.ps1 -All -ErrorAction SilentlyContinue)
    # Resolve only known package/vendor locations adjacent to PATH wrappers.
    # Do not execute npm, inspect credentials or recursively scan disks.
    foreach ($command in $commands) {
        $prefix = Split-Path $command.Source -Parent
        foreach ($relative in @('node_modules\@openai\codex\vendor','node_modules\@openai\codex-win32-x64\vendor','node_modules\@openai\codex-win32-arm64\vendor','node_modules\@openai\codex\node_modules\@openai\codex-win32-x64\vendor','node_modules\@openai\codex\node_modules\@openai\codex-win32-arm64\vendor')) {
            $vendor = Join-Path $prefix $relative
            if (Test-Path -LiteralPath $vendor -PathType Container) {
                foreach ($target in @(Get-ChildItem -LiteralPath $vendor -Directory -ErrorAction SilentlyContinue)) {
                    $paths.Add((Join-Path $target.FullName 'bin\codex.exe'))
                    $paths.Add((Join-Path $target.FullName 'codex\codex.exe'))
                }
            }
        }
    }
    # Prefer npm-native installs inferred from PATH wrappers without running them.
    foreach ($command in $commands) {
        if ([IO.Path]::GetExtension($command.Source) -ieq '.exe') { $paths.Add($command.Source) }
    }
    $desktopRoots = [Collections.Generic.List[string]]::new()
    if ($env:LOCALAPPDATA) { $desktopRoots.Add((Join-Path $env:LOCALAPPDATA 'OpenAI\Codex\bin')) }
    $profileRoot = [Environment]::GetFolderPath('UserProfile')
    if ($profileRoot) { $desktopRoots.Add((Join-Path $profileRoot 'AppData\Local\OpenAI\Codex\bin')) }
    foreach ($desktop in $desktopRoots) {
        if (Test-Path -LiteralPath $desktop -PathType Container) {
            foreach ($dir in @(Get-ChildItem -LiteralPath $desktop -Directory -ErrorAction SilentlyContinue | Sort-Object Name)) {
                $paths.Add((Join-Path $dir.FullName 'codex.exe'))
            }
        }
    }
    $seen = [Collections.Generic.HashSet[string]]::new([StringComparer]::OrdinalIgnoreCase)
    foreach ($path in $paths) {
        if (Test-NativeCodexPath $path) {
            $full = [IO.Path]::GetFullPath($path)
            if ($seen.Add($full)) { Write-Output $full }
        }
    }
}

function Select-NativeCodex([string]$Saved, [switch]$Choose) {
    if (-not $Choose -and (Test-NativeCodexPath $Saved)) { return $Saved }
    $candidates = @(Find-NativeCodex $Saved)
    Write-Host '1 使用自动找到的推荐 Codex CLI；2 自己指定 Codex CLI 路径。'
    if ($candidates.Count -gt 0) { Write-Host "自动推荐：$($candidates[0])" }
    else { Write-Host '没有找到可用的原生程序；不会自动下载或安装。' }
    $default = if ($candidates.Count -gt 0) { '1' } else { '2' }
    $answer = Read-SetupText '请选择 1 或 2' $default
    if ($answer -eq '1' -and $candidates.Count -gt 0) { $path = $candidates[0] }
    elseif ($answer -eq '2') { $path = Read-SetupText '请输入 codex.exe 的完整路径' }
    else { throw '请选择 1 或 2；自动推荐为空时请手动指定。' }
    if (-not [IO.Path]::IsPathRooted($path) -or -not (Test-NativeCodexPath $path)) { throw '请选择存在的 Windows 原生 exe；不接受 ps1/cmd 包装脚本。' }
    return [IO.Path]::GetFullPath($path)
}

function Get-OperatorBase([string]$ProjectRoot) {
    $base = Get-Content -LiteralPath (Join-Path $ProjectRoot 'config.group-assistant.json') -Raw -Encoding UTF8 | ConvertFrom-Json
    # Keep the existing installation exactly. Only a relocated project receives
    # its own project-local default home; never reuse the original user's auth.
    $expected = Join-Path $ProjectRoot 'runtime\codex-home'
    $originalProject = Split-Path (Split-Path $base.CodexHome -Parent) -Parent
    if ([IO.Path]::GetFullPath($ProjectRoot).TrimEnd('\') -ine [IO.Path]::GetFullPath($originalProject).TrimEnd('\')) {
        $base.CodexHome = $expected
        # A copied installation must not require the original user's optional
        # toolchain paths. Only existing local grants survive in the in-memory
        # defaults; never broaden a grant or install a missing tool.
        $base.ToolRoots = @($base.ToolRoots | Where-Object { Test-Path -LiteralPath $_ -PathType Container })
        foreach ($key in @('PythonCommand','NodeCommand','NpmCLI')) {
            if ($null -ne $base.PSObject.Properties[$key] -and $base.$key -and -not (Test-Path -LiteralPath $base.$key -PathType Leaf)) { $base.$key = '' }
        }
    }
    return $base
}

function Merge-OperatorPaths($Base, $Setup = $null) {
    $config = $Base | ConvertTo-Json -Depth 20 | ConvertFrom-Json
    if ($null -ne $Setup -and $null -ne $Setup.PSObject.Properties['OwnerInitialized'] -and $Setup.OwnerInitialized -eq $true) { $config.Owner = $Setup.Owner }
    if ($null -ne $Setup -and $null -ne $Setup.PSObject.Properties['Paths']) {
        foreach ($property in $Setup.Paths.PSObject.Properties) {
            if ($property.Name -notin $script:OperatorPathKeys -or $property.Value -isnot [string] -or -not $property.Value) { throw '路径设置包含未知或无效字段。' }
            $config.($property.Name) = $property.Value
        }
    }
    return $config
}

function Assert-LocalSetupPath([string]$Path, [switch]$AllowDriveRoot) {
    # Reject relative/drive-relative/device/UNC paths and reparse aliases, so
    # source/credential overlap checks cannot be bypassed by a junction.
    if ($Path -notmatch '^[A-Za-z]:[\\/]' -or $Path -match '[\x00-\x1f<>"|?*]' -or $Path.Substring(2).Contains(':')) { throw '目录须为本地磁盘绝对路径，例如 D:\BotWork。' }
    foreach ($part in ($Path.Substring(3) -split '[\\/]')) {
        if ($part -notin @('.','..') -and $part -match '[. ]$') { throw '目录名不能以点或空格结尾。' }
    }
    $full = [IO.Path]::GetFullPath($Path)
    $root = [IO.Path]::GetPathRoot($full)
    if (-not [IO.Directory]::Exists($root) -or ([IO.DriveInfo]::new($root)).DriveType -eq [IO.DriveType]::Network -or (-not $AllowDriveRoot -and $full.TrimEnd('\') -eq $root.TrimEnd('\'))) { throw '磁盘不存在、为网络磁盘或选择了整个磁盘；请选择专用子目录。' }
    $cursor = $full
    while ($cursor -and $cursor -ne $root) {
        if (Test-Path -LiteralPath $cursor) {
            $item = Get-Item -LiteralPath $cursor -Force
            if (-not $item.PSIsContainer -or ($item.Attributes -band [IO.FileAttributes]::ReparsePoint)) { throw '目录不能是文件、目录联接或符号链接。' }
        }
        $cursor = Split-Path $cursor -Parent
    }
    return $full.TrimEnd('\')
}

function Test-SetupOverlap([string]$A, [string]$B) {
    $aPath = [IO.Path]::GetFullPath($A).TrimEnd('\')
    $bPath = [IO.Path]::GetFullPath($B).TrimEnd('\')
    return $aPath.Equals($bPath,[StringComparison]::OrdinalIgnoreCase) -or $aPath.StartsWith($bPath+'\',[StringComparison]::OrdinalIgnoreCase) -or $bPath.StartsWith($aPath+'\',[StringComparison]::OrdinalIgnoreCase)
}

function Assert-OperatorPaths($Base, $Setup) {
    if ($null -eq $Setup.PSObject.Properties['Paths']) { return } # v1 settings stay unchanged.
    $config = Merge-OperatorPaths $Base $Setup
    if (-not (Test-NativeCodexPath $config.CodexCommand)) { throw '所选 Codex exe 已失效，请在菜单重新扫描或指定。' }
    $null = Assert-LocalSetupPath (Split-Path $config.CodexCommand -Parent) -AllowDriveRoot
    if ((Get-Item -LiteralPath $config.CodexCommand -Force).Attributes -band [IO.FileAttributes]::ReparsePoint) { throw 'Codex 程序不能通过文件链接指定。' }
    $dirs = @('DataDir','WorkDir','SoftwareDir','StateDir','CodexHome')
    foreach ($key in $dirs) {
        $path = Assert-LocalSetupPath $config.$key
        if (Test-SetupOverlap $path $config.CodexCommand) { throw 'Codex 程序不能位于工作、状态或认证目录内。' }
        if ($env:SYSTEMROOT -and (Test-SetupOverlap $path $env:SYSTEMROOT)) { throw '工作与认证目录不能包含 Windows 系统目录。' }
        foreach ($reserved in @('backend','source','bin','build-cache','runtime')) {
            $reservedPath = Join-Path $PSScriptRoot $reserved
            if ($key -eq 'CodexHome' -and $reserved -eq 'runtime' -and $path.StartsWith($reservedPath.TrimEnd('\')+'\',[StringComparison]::OrdinalIgnoreCase)) { continue }
            if (Test-SetupOverlap $path $reservedPath) { throw '工作目录不能与项目源码、程序、构建缓存或运行认证目录重叠。' }
        }
    }
    for ($i=0; $i -lt $dirs.Count; $i++) {
        for ($j=$i+1; $j -lt $dirs.Count; $j++) {
            if (Test-SetupOverlap $config.($dirs[$i]) $config.($dirs[$j])) { throw '输出、临时工作、依赖、状态与专用认证目录必须互相分离。' }
        }
    }
    foreach ($tool in $config.ToolRoots) {
        foreach ($key in $dirs) { if (Test-SetupOverlap $config.$key $tool) { throw '工作目录不能与只读工具目录重叠。' } }
    }
    # Existing Go configuration checking additionally resolves aliases and
    # protects personal CodexHome; it remains mandatory before save/start/login.
}

function Read-OperatorPaths($Base, $Setup = $null, [switch]$ChooseCodex) {
    $config = Merge-OperatorPaths $Base $Setup
    $codex = Select-NativeCodex $config.CodexCommand -Choose:$ChooseCodex
    $defaultRoot = Split-Path $config.DataDir -Parent
    Write-Host "输出：$($config.DataDir)；临时工作：$($config.WorkDir)"
    $root = Read-SetupText '工作根目录（回车保留当前各目录）' $defaultRoot
    try { $null = Assert-LocalSetupPath $root }
    catch {
        Write-Host '默认或输入的目录不可用，请指定当前电脑的有效本地目录。'
        $root = Read-SetupText '工作根目录（完整路径）'
        $null = Assert-LocalSetupPath $root
    }
    $paths = [ordered]@{ CodexCommand=$codex; DataDir=$config.DataDir; WorkDir=$config.WorkDir; SoftwareDir=$config.SoftwareDir; StateDir=$config.StateDir; CodexHome=$config.CodexHome }
    if ($root -ine $defaultRoot) {
        $root = Assert-LocalSetupPath $root
        foreach ($entry in @(@('DataDir','files'),@('WorkDir','tmp'),@('SoftwareDir','software'),@('StateDir','state'))) { $paths[$entry[0]] = Join-Path $root $entry[1] }
    }
    if ((Read-SetupText '高级设置：分别指定各目录？Y/N' 'N') -ieq 'Y') {
        foreach ($key in @('DataDir','WorkDir','SoftwareDir','StateDir','CodexHome')) {
            $paths[$key] = Read-SetupText "$key 目录（回车保留）" $paths[$key]
        }
    }
    Write-Host '改变目录不会复制、搬迁或删除旧资料；旧文件/会话仅在原路径保留。'
    return [pscustomobject]$paths
}
