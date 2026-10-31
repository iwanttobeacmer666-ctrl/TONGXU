param([string]$ProjectRoot = (Split-Path $PSScriptRoot -Parent))
$ErrorActionPreference = 'Stop'
. (Join-Path $ProjectRoot 'Operator-Setup.ps1')
$script:pathAnswers = [Collections.Generic.Queue[string]]::new()
function Read-SetupText([string]$Prompt,[string]$Default='') {
    if ($script:pathAnswers.Count -eq 0) { throw "路径测试缺少回答：$Prompt" }
    $answer=$script:pathAnswers.Dequeue()
    if ($answer -eq '/cancel') { throw [OperationCanceledException]::new() }
    if ($answer -eq '') { return $Default }
    return $answer
}
function Assert-PathCheck([bool]$Condition,[string]$Message) { if (-not $Condition) { throw $Message } }
function Assert-PathRejected([scriptblock]$Action) {
    $rejected=$false
    try { & $Action | Out-Null } catch { $rejected=$true }
    Assert-PathCheck $rejected '应拒绝非法路径。'
}
$fixture=Join-Path $PSScriptRoot ('paths-fixture-'+[guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $fixture | Out-Null
try {
    $base=Get-OperatorBase $ProjectRoot
    $diskBase=Get-Content (Join-Path $ProjectRoot 'config.group-assistant.json') -Raw -Encoding UTF8 | ConvertFrom-Json
    Assert-PathCheck (($base | ConvertTo-Json -Depth 20) -ceq ($diskBase | ConvertTo-Json -Depth 20)) '本机默认配置被改变。'
    # Public templates intentionally grant no host tool directories. Exercise
    # tool-overlap checks using a test-owned directory, not machine settings.
    $base.ToolRoots = @((Join-Path $fixture 'fixture-tools'))
    New-Item -ItemType Directory -Path $base.ToolRoots[0] -Force | Out-Null
    # Use an existing PE fixture for path-only checks; this is never run as Codex.
    # Production configuration and its potentially stale selected path stay intact.
    $base.CodexCommand=Join-Path $ProjectRoot 'bin\group-assistant.exe'
    $before=$base | ConvertTo-Json -Depth 20
    $legacy=[pscustomobject]@{Version=1;Owner=$base.Owner;BotID='fixture';ProtectedSecret='not-decrypted';Groups=@([pscustomobject]@{ID='g';Enabled=$true;Members=@()})}
    $effective=Merge-OperatorPaths $base $legacy
    Assert-PathCheck (($effective | ConvertTo-Json -Depth 20) -ceq $before) '旧版设置改变了路径。'
    foreach ($answer in @('','')) { $script:pathAnswers.Enqueue($answer) }
    $defaults=Read-OperatorPaths $base
    Assert-PathCheck ($defaults.DataDir -ceq $base.DataDir -and $defaults.WorkDir -ceq $base.WorkDir -and $defaults.CodexHome -ceq $base.CodexHome -and $defaults.CodexCommand -ceq $base.CodexCommand) '回车没有保留本机路径。'
    $root=Join-Path $fixture 'chosen work'
    foreach ($answer in @($root,'N')) { $script:pathAnswers.Enqueue($answer) }
    $paths=Read-OperatorPaths $base
    $legacy | Add-Member -NotePropertyName Paths -NotePropertyValue $paths
    Assert-OperatorPaths $base $legacy
    Assert-PathCheck ($paths.DataDir -ceq (Join-Path $root 'files') -and $paths.StateDir -ceq (Join-Path $root 'state')) '工作根目录映射错误。'
    Assert-PathCheck (-not (Test-Path -LiteralPath $root)) '配置选择不应提前创建用户工作目录。'
    foreach ($answer in @('relative',$root,'N')) { $script:pathAnswers.Enqueue($answer) }
    $repairedRoot=Read-OperatorPaths $base
    Assert-PathCheck ($repairedRoot.DataDir -ceq (Join-Path $root 'files')) '无效默认/输入目录没有要求重新选择。'
    foreach ($answer in @($root,'Y',(Join-Path $fixture 'outputs'),(Join-Path $fixture 'cache'),(Join-Path $fixture 'software'),(Join-Path $fixture 'state'),$base.CodexHome)) { $script:pathAnswers.Enqueue($answer) }
    $advanced=Read-OperatorPaths $base
    $legacy.Paths=$advanced
    Assert-OperatorPaths $base $legacy
    $binary=Join-Path $ProjectRoot 'bin\group-assistant.exe'
    Test-EffectiveConfig $binary $base $legacy $fixture
    Assert-PathCheck (-not (Test-Path -LiteralPath $advanced.DataDir)) '检查配置提前写入了工作目录。'
    $invalid=$advanced | ConvertTo-Json | ConvertFrom-Json
    $invalid.WorkDir=$invalid.DataDir
    $legacy.Paths=$invalid
    Assert-PathRejected { Assert-OperatorPaths $base $legacy }
    $invalid=$advanced | ConvertTo-Json | ConvertFrom-Json
    $invalid.DataDir=Join-Path $ProjectRoot 'source\disallowed'
    $legacy.Paths=$invalid
    Assert-PathRejected { Assert-OperatorPaths $base $legacy }
    $invalid=$advanced | ConvertTo-Json | ConvertFrom-Json
    $invalid.DataDir=$base.ToolRoots[0]
    $legacy.Paths=$invalid
    Assert-PathRejected { Assert-OperatorPaths $base $legacy }
    $target=Join-Path $fixture 'junction-target'
    $alias=Join-Path $fixture 'junction-alias'
    New-Item -ItemType Directory -Path $target | Out-Null
    $junctionCreated=$false
    try { New-Item -ItemType Junction -Path $alias -Target $target -ErrorAction Stop | Out-Null; $junctionCreated=$true }
    catch { Write-Output 'SKIP: junction creation unavailable in this test environment.' }
    if ($junctionCreated) { Assert-PathRejected { Assert-LocalSetupPath (Join-Path $alias 'files') } }
    foreach ($bad in @('relative','D:relative','\\server\share\folder','\\?\D:\folder',[IO.Path]::GetPathRoot($fixture),(Join-Path $fixture 'source.\files'))) { Assert-PathRejected { Assert-LocalSetupPath $bad } }
    $invalid=[pscustomobject]@{DataDir=(Join-Path $fixture 'valid');Unknown='bad'}
    $legacy.Paths=$invalid
    Assert-PathRejected { Merge-OperatorPaths $base $legacy }
    $invalid=$advanced | ConvertTo-Json | ConvertFrom-Json
    $invalid.CodexHome=Join-Path ([Environment]::GetFolderPath('UserProfile')) '.codex'
    $legacy.Paths=$invalid
    Assert-PathRejected { Test-EffectiveConfig $binary $base $legacy $fixture }
    # Discovery is file inspection only, returns native PE files and deduplicates.
    $found=@(Find-NativeCodex $base.CodexCommand)
    Assert-PathCheck ($found -contains [IO.Path]::GetFullPath($base.CodexCommand)) '发现列表漏掉当前有效 Codex。'
    foreach ($candidate in $found) { Assert-PathCheck (Test-NativeCodexPath $candidate) '发现列表包含包装器。' }
    Assert-PathCheck (-not (Test-NativeCodexPath (Join-Path $fixture 'codex.cmd'))) '包装器被视为原生 exe。'
    Assert-PathCheck ((Select-NativeCodex $base.CodexCommand) -ceq $base.CodexCommand) '已有有效路径未优先使用。'
    $script:pathAnswers.Enqueue('2')
    $script:pathAnswers.Enqueue($base.CodexCommand)
    Assert-PathCheck ((Select-NativeCodex (Join-Path $fixture 'missing.exe')) -ceq [IO.Path]::GetFullPath($base.CodexCommand)) '失效路径无法重新选择。'
    $script:pathAnswers.Enqueue('/cancel')
    Assert-PathRejected { Select-NativeCodex '' -Choose }
    # PATH-wrapper discovery must prefer existing npm-native layouts, never
    # execute a wrapper, and expose one recommendation rather than a candidates menu.
    $npmFixture=Join-Path $fixture 'npm-prefix'
    $npmBin=Join-Path $npmFixture 'node_modules\@openai\codex-win32-x64\vendor\x86_64-pc-windows-msvc\bin\codex.exe'
    $npmLegacy=Join-Path $npmFixture 'node_modules\@openai\codex-win32-x64\vendor\x86_64-pc-windows-msvc\codex\codex.exe'
    New-Item -ItemType Directory -Path (Split-Path $npmBin -Parent),(Split-Path $npmLegacy -Parent) -Force | Out-Null
    Copy-Item -LiteralPath $base.CodexCommand -Destination $npmBin
    Copy-Item -LiteralPath $base.CodexCommand -Destination $npmLegacy
    [IO.File]::WriteAllText((Join-Path $npmFixture 'codex.cmd'),'@exit /b 99',[Text.Encoding]::ASCII)
    $beforePath=$env:PATH
    try {
        $env:PATH=$npmFixture+';'+$beforePath
        $candidates=@(Find-NativeCodex)
        Assert-PathCheck ($candidates.Count -ge 2 -and $candidates[0] -ceq $npmBin -and $candidates -contains $npmLegacy) 'npm bin/legacy 发现或优先级错误。'
        $script:pathAnswers.Enqueue('1')
        Assert-PathCheck ((Select-NativeCodex '') -ceq $npmBin) '自动第一项不是推荐程序。'
        foreach($answer in @('2',$npmLegacy)){$script:pathAnswers.Enqueue($answer)}
        Assert-PathCheck ((Select-NativeCodex '' -Choose) -ceq $npmLegacy) '手动第二项无法指定程序。'
        $script:pathAnswers.Enqueue('3')
        Assert-PathRejected { Select-NativeCodex '' -Choose }
        foreach($answer in @('2',(Join-Path $npmFixture 'codex.cmd'))){$script:pathAnswers.Enqueue($answer)}
        Assert-PathRejected { Select-NativeCodex '' -Choose }
    } finally { $env:PATH=$beforePath }
    # Relocated copies get a separate default auth home; disk config stays intact.
    $copy=Join-Path $fixture 'copied-project'
    New-Item -ItemType Directory -Path $copy | Out-Null
    Copy-Item -LiteralPath (Join-Path $ProjectRoot 'config.group-assistant.json') -Destination (Join-Path $copy 'config.group-assistant.json')
    $copyBase=$base | ConvertTo-Json -Depth 20 | ConvertFrom-Json
    $missingTool=Join-Path $copy 'missing-tools'
    $copyBase.ToolRoots=@($copyBase.ToolRoots)+$missingTool
    $copyBase.PythonCommand=Join-Path $missingTool 'python.exe'
    Write-PrivateJson (Join-Path $copy 'config.group-assistant.json') $copyBase
    $relocated=Get-OperatorBase $copy
    Assert-PathCheck ($relocated.CodexHome -ceq (Join-Path $copy 'runtime\codex-home')) '复制的项目重用了原认证目录。'
    Assert-PathCheck ($relocated.PythonCommand -ceq '' -and $relocated.ToolRoots -notcontains $missingTool) '复制项目仍强制要求不存在的原工具。'
    $copyDisk=Get-Content -LiteralPath (Join-Path $copy 'config.group-assistant.json') -Raw -Encoding UTF8 | ConvertFrom-Json
    Assert-PathCheck ($copyDisk.PythonCommand -ceq $copyBase.PythonCommand) '复制项目的根配置被自动改写。'
    Assert-PathCheck (($base | ConvertTo-Json -Depth 20) -ceq $before) '基础对象被路径选择修改。'
    Assert-PathCheck ($script:pathAnswers.Count -eq 0) '路径回答未消费完。'
    Write-Output 'PASS: existing defaults/v1 preservation, root/advanced paths, no premature writes, overlap/source/tool/personal-home rejection, native discovery/manual repair/cancel, relocated home. No Codex execution or login.'
} finally {
    $resolved=(Resolve-Path -LiteralPath $fixture).Path
    $parent=(Resolve-Path -LiteralPath $PSScriptRoot).Path
    if ([IO.Path]::GetDirectoryName($resolved) -ne $parent -or -not [IO.Path]::GetFileName($resolved).StartsWith('paths-fixture-')) { throw 'Unexpected cleanup target.' }
    Remove-Item -LiteralPath $resolved -Recurse
}
