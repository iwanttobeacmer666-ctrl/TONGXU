param([string]$ProjectRoot = (Split-Path $PSScriptRoot -Parent), [string]$Go = 'D:\Go\bin\go.exe')
$ErrorActionPreference = 'Stop'
. (Join-Path $ProjectRoot 'Operator-Setup.ps1')
$script:answers = [Collections.Generic.Queue[string]]::new()
function Read-SetupText([string]$Prompt, [string]$Default = '') {
    if ($script:answers.Count -eq 0) { throw "测试缺少回答：$Prompt" }
    return $script:answers.Dequeue()
}
function Assert-Check([bool]$Condition, [string]$Message) { if (-not $Condition) { throw $Message } }
function Assert-Rejected([scriptblock]$Action) {
    $rejected = $false
    try { & $Action | Out-Null } catch { $rejected = $true }
    Assert-Check $rejected '应拒绝无效操作。'
}
$taskDirectory = Join-Path $ProjectRoot ('validation\operator-fixture-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $taskDirectory | Out-Null
try {
    $base = Get-Content (Join-Path $ProjectRoot 'config.group-assistant.json') -Raw -Encoding UTF8 | ConvertFrom-Json
    # Config fixtures inspect a native PE but never execute it as Codex. Do
    # not depend on a desktop version directory surviving an app update.
    $base.CodexCommand = Join-Path $ProjectRoot 'bin\group-assistant.exe'
    $baseBefore = $base | ConvertTo-Json -Depth 20
    $plainFixture = 'local-fixture-only-not-a-credential'
    $secure = ConvertTo-SecureString $plainFixture -AsPlainText -Force
    try { $cipher = ConvertFrom-SecureString $secure } finally { $secure.Dispose() }
    $decoded = ConvertTo-SecureString $cipher
    try { Assert-Check ([Net.NetworkCredential]::new('', $decoded).Password -ceq $plainFixture) 'DPAPI 往返失败。' }
    finally { $decoded.Dispose() }
    $setup = [pscustomobject]@{
        Version = 1; Owner = $base.Owner; BotID = 'fixture-bot'; ProtectedSecret = $cipher
        Groups = @(
            [pscustomobject]@{ID='fixture-a'; Enabled=$true; Members=@([pscustomobject]@{ID='alice'; Enabled=$true},[pscustomobject]@{ID='bob'; Enabled=$false})},
            [pscustomobject]@{ID='fixture-b'; Enabled=$false; Members=@([pscustomobject]@{ID='alice'; Enabled=$true})}
        )
    }
    function Read-SetupSecret { return $cipher }
    foreach ($answer in @('fixture-bot',$base.Owner,'2','wizard-a','0','wizard-b','1','alice','1',(Split-Path $base.DataDir -Parent),'N')) { $script:answers.Enqueue($answer) }
    $wizard = New-OperatorSetup $base
    Assert-OperatorSetup $wizard $base
    Assert-Check (@($wizard.Groups[0].Members).Count -eq 0 -and @($wizard.Groups[1].Members).Count -eq 1) '首次向导零成员/多群录入失败。'
    foreach ($answer in @('fixture-bot','new-owner','1','fixture-new','0','1',(Split-Path $base.DataDir -Parent),'N')) { $script:answers.Enqueue($answer) }
    $newWizard = New-OperatorSetup $base
    Assert-OperatorSetup $newWizard $base
    Assert-Check ((New-EffectiveConfig $base $newWizard).Owner -ceq 'new-owner') '新部署管理员未应用。'
    $newOwnerPath = Join-Path $taskDirectory 'new-owner.json'
    $script:answers.Enqueue('SAVE')
    Assert-Check (Save-OperatorSetup $newOwnerPath $newWizard $base (Join-Path $ProjectRoot 'bin\group-assistant.exe')) '新管理员保存失败。'
    $newWizard.Owner = 'transfer-attempt'
    Assert-Rejected { Save-OperatorSetup $newOwnerPath $newWizard $base (Join-Path $ProjectRoot 'bin\group-assistant.exe') }
    $effective = New-EffectiveConfig $base $setup
    Assert-Check (@($effective.Groups).Count -eq 1 -and $effective.Groups[0] -ceq 'fixture-a') '停用群仍启用。'
    Assert-Check (@($effective.Members['fixture-a']).Count -eq 1 -and $effective.Members['fixture-a'][0] -ceq 'alice') '成员准入过滤错误。'
    Assert-Check (-not $effective.BoundaryVerified -and ($base | ConvertTo-Json -Depth 20) -ceq $baseBefore) '原配置被修改。'
    Assert-Check ($effective.OwnerOnly) '默认检查配置未限制管理员试用。'
    $formal = New-EffectiveConfig $base $setup -Formal
    Assert-Check ($formal.BoundaryVerified -and -not $formal.OwnerOnly) '正式启动未启用名册成员。'
    Assert-Check (@($formal.Groups).Count -eq 1 -and @($formal.Members['fixture-a']).Count -eq 1 -and $formal.Members['fixture-a'][0] -ceq 'alice') '正式模式绕过群/成员停用状态。'
    Assert-Check (($base | ConvertTo-Json -Depth 20) -ceq $baseBefore) '正式配置修改了根对象。'
    $path = Join-Path $taskDirectory 'setup.json'
    $binary = Join-Path $ProjectRoot 'bin\group-assistant.exe'
    $script:answers.Enqueue('SAVE')
    $result = Save-OperatorSetup $path $setup $base $binary
    Assert-Check ($result -is [bool] -and $result) '首次原子保存失败或输出污染返回值。'
    $persisted = [IO.File]::ReadAllText($path)
    Assert-Check (-not $persisted.Contains($plainFixture)) '测试 Secret 被明文保存。'
    $loaded = Read-OperatorSetup $path $base
    Assert-Check ($loaded.BotID -ceq $setup.BotID -and @($loaded.Groups).Count -eq 2) '重载丢失设置。'
    $script:answers.Enqueue('SAVE')
    Assert-Check (Save-OperatorSetup $path $loaded $base $binary) '替换原设置失败。'
    $before = [IO.File]::ReadAllText($path)
    Assert-Rejected { Save-OperatorSetup $path $loaded $base $binary -ExpectedVersion 'stale-window-version' }
    Assert-Check ([IO.File]::ReadAllText($path) -ceq $before) '旧窗口覆盖了新配置。'
    $script:answers.Enqueue('no')
    $result = Save-OperatorSetup $path $loaded $base $binary
    Assert-Check ($result -is [bool] -and -not $result -and [IO.File]::ReadAllText($path) -ceq $before) '取消后原设置被覆盖。'
    $loaded.Owner = 'someone-else'
    Assert-Rejected { Save-OperatorSetup $path $loaded $base $binary }
    Assert-Check ([IO.File]::ReadAllText($path) -ceq $before) '非法管理员转交改变设置。'
    $loaded = Read-OperatorSetup $path $base
    $loaded.Groups[0].Members += [pscustomobject]@{ID='alice'; Enabled=$true}
    Assert-Rejected { Assert-OperatorSetup $loaded $base }
    $loaded = Read-OperatorSetup $path $base
    $loaded.Groups[1].ID = 'fixture-a'
    Assert-Rejected { Assert-OperatorSetup $loaded $base }
    $loaded = Read-OperatorSetup $path $base
    foreach ($group in $loaded.Groups) { $group.Enabled=$false }
    Assert-Check (@((New-EffectiveConfig $base $loaded).Groups).Count -eq 0) '所有群停用时应保持空准入名单，供本地管理恢复。'
    Test-EffectiveConfig $binary $base $loaded $taskDirectory
    # Zero members and same ID in separate groups are both valid.
    $loaded.Groups[0].Members = @()
    Assert-OperatorSetup $loaded $base
    foreach ($bad in @('', ' user', 'bad id', 'wecom:user', "bad`nuser")) { Assert-Rejected { Assert-SetupID $bad } }
    # Menu mutations operate on a draft; cancellation cannot mutate live settings.
    $loaded = Read-OperatorSetup $path $base
    foreach ($answer in @('7','1','1')) { $script:answers.Enqueue($answer) }
    $draft = Edit-OperatorSetup $loaded
    Assert-Check (-not $draft.Groups[0].Members[0].Enabled -and $loaded.Groups[0].Members[0].Enabled) '菜单修改污染原对象。'
    foreach ($answer in @('2','fixture-c','0')) { $script:answers.Enqueue($answer) }
    $draft = Edit-OperatorSetup $loaded
    Assert-OperatorSetup $draft $base
    Assert-Check (@($draft.Groups).Count -eq 3 -and @($draft.Groups[2].Members).Count -eq 0) '添加空成员群失败。'
    foreach ($answer in @('5','1','1','carol')) { $script:answers.Enqueue($answer) }
    $draft = Edit-OperatorSetup $loaded
    Assert-OperatorSetup $draft $base
    Assert-Check (@($draft.Groups[0].Members).Count -eq 3) '添加成员失败。'
    foreach ($answer in @('3','2','renamed-group')) { $script:answers.Enqueue($answer) }
    $draft = Edit-OperatorSetup $loaded
    Assert-Check ($draft.Groups[1].ID -ceq 'renamed-group') '修改群 ID 失败。'
    foreach ($answer in @('4','2')) { $script:answers.Enqueue($answer) }
    $draft = Edit-OperatorSetup $loaded
    Assert-Check $draft.Groups[1].Enabled '启用群失败。'
    foreach ($answer in @('6','1','2','renamed-user')) { $script:answers.Enqueue($answer) }
    $draft = Edit-OperatorSetup $loaded
    Assert-Check ($draft.Groups[0].Members[1].ID -ceq 'renamed-user') '修改成员失败。'
    foreach ($answer in @('1','new-fixture-bot')) { $script:answers.Enqueue($answer) }
    $draft = Edit-OperatorSetup $loaded
    Assert-Check ($draft.BotID -ceq 'new-fixture-bot' -and $draft.ProtectedSecret -ceq $cipher) '更换凭据失败。'
    $script:answers.Enqueue('0')
    Assert-Check ($null -eq (Edit-OperatorSetup $loaded)) '菜单返回没有取消。'
    [IO.File]::WriteAllText($path, '{broken', [Text.UTF8Encoding]::new($false))
    Assert-Rejected { Read-OperatorSetup $path $base }
    Assert-Check ([IO.File]::ReadAllText($path) -ceq '{broken') '损坏设置被静默重置。'
    # Deliberately corrupt ciphertext. Startup must fail before invoking a process.
    $loaded.ProtectedSecret = 'invalid-ciphertext'
    Assert-Rejected { Start-OperatorTrial 'NEVER-RUN-THIS.exe' $base $loaded $taskDirectory }
    Assert-Check (@(Get-ChildItem -LiteralPath $taskDirectory -Filter '.operator-*').Count -eq 0) '临时配置未清理。'
    # Compile an existing-tool-only local child fixture. Network fetching is
    # disabled and every process environment change is restored in finally.
    $previous = @{}
    $values = @{
        GOPROXY='off'; GOTOOLCHAIN='local'; CGO_ENABLED='0';
        GOMODCACHE=(Join-Path $ProjectRoot 'build-cache\modules');
        GOCACHE=(Join-Path $ProjectRoot 'build-cache\go-build');
        GOTMPDIR=$taskDirectory; TEMP=$taskDirectory; TMP=$taskDirectory;
        CC_OPERATOR_FIXTURE_REPORT=(Join-Path $taskDirectory 'child-report.txt');
        CC_OPERATOR_FIXTURE_COUNT=(Join-Path $taskDirectory 'child-count.txt');
        CC_OPERATOR_FIXTURE_FAIL='0'; CC_OPERATOR_FIXTURE_PROBE_FAIL='0'
    }
    foreach ($key in $values.Keys) { $previous[$key] = [Environment]::GetEnvironmentVariable($key,'Process') }
    $previousID = [Environment]::GetEnvironmentVariable('CC_GROUP_BOT_ID','Process')
    $previousSecret = [Environment]::GetEnvironmentVariable('CC_GROUP_BOT_SECRET','Process')
    try {
        foreach ($key in $values.Keys) { [Environment]::SetEnvironmentVariable($key,$values[$key],'Process') }
        $fixtureBinary = Join-Path $taskDirectory 'operator-child.exe'
        & $Go build -o $fixtureBinary (Join-Path $ProjectRoot 'validation\operator-process-fixture.go')
        if ($LASTEXITCODE -ne 0) { throw 'Offline child fixture build failed.' }
        $loaded = $setup | ConvertTo-Json -Depth 20 | ConvertFrom-Json
        Start-OperatorTrial $fixtureBinary $base $loaded $taskDirectory
        Assert-Check (Test-Path -LiteralPath $values.CC_OPERATOR_FIXTURE_REPORT -PathType Leaf) '管理员试用子进程未收到虚构凭据。'
        Assert-Check ((Get-Content -LiteralPath $values.CC_OPERATOR_FIXTURE_REPORT -Raw).StartsWith('owner-only')) '试用模式未限制管理员。'
        Start-OperatorService $fixtureBinary $base $loaded $taskDirectory -Mode Formal
        Assert-Check ((Get-Content -LiteralPath $values.CC_OPERATOR_FIXTURE_REPORT -Raw).StartsWith('formal')) '正式模式未传递正式运行配置。'
        Assert-Check ([Environment]::GetEnvironmentVariable('CC_GROUP_BOT_ID','Process') -ceq $previousID) '父进程 Bot ID 被修改。'
        Assert-Check ([Environment]::GetEnvironmentVariable('CC_GROUP_BOT_SECRET','Process') -ceq $previousSecret) '父进程 Secret 被修改。'
        Assert-Check (@(Get-ChildItem -LiteralPath $taskDirectory -Filter '.operator-*').Count -eq 0) '启动临时配置未清理。'
        # Run the complete new and compatibility entry points in a disposable
        # project with a fake binary and queued console input; never a real bot.
        $app = Join-Path $taskDirectory 'app'
        New-Item -ItemType Directory -Path (Join-Path $app 'bin'),(Join-Path $app 'runtime') | Out-Null
        foreach ($scriptName in @('Start-GroupAssistant.ps1','Configure-GroupAssistant.ps1','Operator-Setup.ps1','Operator-Paths.ps1','Test-Office.ps1','Login-Codex.ps1')) {
            Copy-Item -LiteralPath (Join-Path $ProjectRoot $scriptName) -Destination (Join-Path $app $scriptName)
        }
        Copy-Item -LiteralPath $fixtureBinary -Destination (Join-Path $app 'bin\group-assistant.exe')
        Write-PrivateJson (Join-Path $app 'config.group-assistant.json') $base
        $taskConsoleQueue = $script:answers
        function Read-Host([string]$Prompt, [switch]$AsSecureString) {
            if ($AsSecureString) { return ConvertTo-SecureString $plainFixture -AsPlainText -Force }
            if ($taskConsoleQueue.Count -eq 0) { throw "入口测试缺少回答：$Prompt" }
            return $taskConsoleQueue.Dequeue()
        }
        $countBefore=[int](Get-Content -LiteralPath $values.CC_OPERATOR_FIXTURE_COUNT -Raw)
        foreach ($answer in @('fixture-bot','', '1','fixture-a','1','alice','1',(Split-Path $base.DataDir -Parent),'N','SAVE')) { $script:answers.Enqueue($answer) }
        & (Join-Path $app 'Start-GroupAssistant.ps1')
        $saved = Join-Path $app 'runtime\operator-setup.json'
        Assert-Check (Test-Path -LiteralPath $saved -PathType Leaf) '首次入口未保存。'
        Assert-Check ($script:answers.Count -eq 0 -and [int](Get-Content -LiteralPath $values.CC_OPERATOR_FIXTURE_COUNT -Raw) -eq $countBefore+1) '首次保存后未直接启动一次。'
        Assert-Check ((Get-Content -LiteralPath $values.CC_OPERATOR_FIXTURE_REPORT -Raw).StartsWith('formal')) '首次保存后未使用正式模式。'
        $countBefore=[int](Get-Content -LiteralPath $values.CC_OPERATOR_FIXTURE_COUNT -Raw)
        & (Join-Path $app 'Start-GroupAssistant.ps1')
        Assert-Check ($script:answers.Count -eq 0 -and [int](Get-Content -LiteralPath $values.CC_OPERATOR_FIXTURE_COUNT -Raw) -eq $countBefore+1) '已配置入口未无菜单启动一次。'
        & (Join-Path $app 'Test-Office.ps1')
        Assert-Check ($script:answers.Count -eq 0 -and [int](Get-Content -LiteralPath $values.CC_OPERATOR_FIXTURE_COUNT -Raw) -eq $countBefore+2) '旧入口未直接正式启动。'
        # Explicit configuration entry must never start a service.
        $countBefore=[int](Get-Content -LiteralPath $values.CC_OPERATOR_FIXTURE_COUNT -Raw)
        foreach ($answer in @('3','0')) { $script:answers.Enqueue($answer) }
        & (Join-Path $app 'Configure-GroupAssistant.ps1')
        Assert-Check ($script:answers.Count -eq 0 -and [int](Get-Content -LiteralPath $values.CC_OPERATOR_FIXTURE_COUNT -Raw) -eq $countBefore) '配置入口启动了服务。'
        # Canceling first configuration must not persist or start anything.
        $canceledApp=Join-Path $taskDirectory 'cancel-app'
        New-Item -ItemType Directory -Path (Join-Path $canceledApp 'bin'),(Join-Path $canceledApp 'runtime') | Out-Null
        foreach($scriptName in @('Start-GroupAssistant.ps1','Operator-Setup.ps1','Operator-Paths.ps1')) {
            Copy-Item -LiteralPath (Join-Path $ProjectRoot $scriptName) -Destination (Join-Path $canceledApp $scriptName)
        }
        Copy-Item -LiteralPath $fixtureBinary -Destination (Join-Path $canceledApp 'bin\group-assistant.exe')
        Write-PrivateJson (Join-Path $canceledApp 'config.group-assistant.json') $base
        foreach($answer in @('fixture-bot','','1','fixture-a','1','alice','1',(Split-Path $base.DataDir -Parent),'N','NO')) { $script:answers.Enqueue($answer) }
        & (Join-Path $canceledApp 'Start-GroupAssistant.ps1')
        Assert-Check ($script:answers.Count -eq 0 -and -not (Test-Path -LiteralPath (Join-Path $canceledApp 'runtime\operator-setup.json')) -and [int](Get-Content -LiteralPath $values.CC_OPERATOR_FIXTURE_COUNT -Raw) -eq $countBefore) '取消首次保存仍保存或启动。'
        # Child failure exits instead of returning to a menu or restarting.
        $env:CC_OPERATOR_FIXTURE_FAIL='1'
        try { Assert-Rejected { & (Join-Path $app 'Start-GroupAssistant.ps1') } }
        finally { $env:CC_OPERATOR_FIXTURE_FAIL='0' }
        Assert-Check ($script:answers.Count -eq 0 -and [int](Get-Content -LiteralPath $values.CC_OPERATOR_FIXTURE_COUNT -Raw) -eq $countBefore+1) '异常退出后发生了重启或提示。'
        # A recovered selection is saved after the probe. The next run stays
        # silent, and a later invalidation produces the same two choices again.
        $fixtureSetup=Read-OperatorSetup $saved $base
        $fixtureBefore=[IO.File]::ReadAllText($saved)
        $fixtureSetup.Paths.CodexCommand=Join-Path $app 'missing.exe'
        Write-PrivateJson $saved $fixtureSetup
        $fixtureBroken=[IO.File]::ReadAllText($saved)
        foreach($answer in @('2',$fixtureBinary)){$script:answers.Enqueue($answer)}
        & (Join-Path $app 'Start-GroupAssistant.ps1')
        $recovered=Read-OperatorSetup $saved $base
        Assert-Check ($recovered.Paths.CodexCommand -ceq $fixtureBinary -and $script:answers.Count -eq 0) '手动选择没有保存新路径。'
        $compareSaved=$recovered|ConvertTo-Json -Depth 20|ConvertFrom-Json
        $compareOld=$fixtureSetup|ConvertTo-Json -Depth 20|ConvertFrom-Json
        $compareSaved.Paths.CodexCommand='';$compareOld.Paths.CodexCommand=''
        Assert-Check (($compareSaved|ConvertTo-Json -Depth 20) -ceq ($compareOld|ConvertTo-Json -Depth 20)) '路径保存更改了其他成员/凭据/设置。'
        Assert-Check ([int](Get-Content -LiteralPath $values.CC_OPERATOR_FIXTURE_COUNT -Raw) -eq $countBefore+2) '失效路径手动选择未启动一次。'
        & (Join-Path $app 'Start-GroupAssistant.ps1')
        Assert-Check ($script:answers.Count -eq 0 -and [int](Get-Content -LiteralPath $values.CC_OPERATOR_FIXTURE_COUNT -Raw) -eq $countBefore+3) '保存后仍询问路径。'
        $recovered.Paths.CodexCommand=Join-Path $app 'missing-again.exe'
        Write-PrivateJson $saved $recovered
        $script:answers.Enqueue('1')
        & (Join-Path $app 'Start-GroupAssistant.ps1')
        $recovered=Read-OperatorSetup $saved $base
        Assert-Check ((Test-NativeCodexPath $recovered.Paths.CodexCommand) -and $script:answers.Count -eq 0) '自动推荐没有保存有效路径。'
        Assert-Check ([int](Get-Content -LiteralPath $values.CC_OPERATOR_FIXTURE_COUNT -Raw) -eq $countBefore+4) '再次失效未重新选择。'
        & (Join-Path $app 'Start-GroupAssistant.ps1')
        Assert-Check ($script:answers.Count -eq 0 -and [int](Get-Content -LiteralPath $values.CC_OPERATOR_FIXTURE_COUNT -Raw) -eq $countBefore+5) '自动保存后仍询问路径。'
        foreach($answer in @('2',(Join-Path $app 'still-missing.exe'))){$script:answers.Enqueue($answer)}
        Assert-Rejected { & (Join-Path $app 'Start-GroupAssistant.ps1') -ChooseCodex }
        Assert-Check ([int](Get-Content -LiteralPath $values.CC_OPERATOR_FIXTURE_COUNT -Raw) -eq $countBefore+5) '无效手动路径启动了服务。'
        $beforeProbeFailure=[IO.File]::ReadAllText($saved)
        $env:CC_OPERATOR_FIXTURE_PROBE_FAIL='1'
        foreach($answer in @('2',$fixtureBinary)){$script:answers.Enqueue($answer)}
        try { Assert-Rejected { & (Join-Path $app 'Start-GroupAssistant.ps1') -ChooseCodex } }
        finally { $env:CC_OPERATOR_FIXTURE_PROBE_FAIL='0' }
        Assert-Check ([IO.File]::ReadAllText($saved) -ceq $beforeProbeFailure -and [int](Get-Content -LiteralPath $values.CC_OPERATOR_FIXTURE_COUNT -Raw) -eq $countBefore+5) '握手失败仍保存路径或启动服务。'
        foreach($answer in @('2',$fixtureBinary)){$script:answers.Enqueue($answer)}
        & (Join-Path $app 'Start-GroupAssistant.ps1') -ChooseCodex
        Assert-Check ($script:answers.Count -eq 0 -and (Read-OperatorSetup $saved $base).Paths.CodexCommand -ceq $fixtureBinary) '显式手动选择未保存路径。'
        $beforeConflict=[IO.File]::ReadAllText($saved)
        $selectionLock=Open-OperatorLock (Split-Path $saved -Parent)
        try { Assert-Rejected { Save-SelectedCodexUnderLock $saved $base $fixtureBinary 'stale-version' } }
        finally { $selectionLock.Dispose() }
        $liveLock=Open-OperatorLock (Split-Path $saved -Parent) 'service.lock'
        try { Assert-Rejected { Save-SelectedCodexUnderLock $saved $base $fixtureBinary (Get-FileHash -LiteralPath $saved -Algorithm SHA256).Hash.ToLowerInvariant() } }
        finally { $liveLock.Dispose() }
        Assert-Check ([IO.File]::ReadAllText($saved) -ceq $beforeConflict) '路径冲突或服务锁失败仍覆盖设置。'
        foreach ($answer in @('2','7','1','1','SAVE','0')) { $script:answers.Enqueue($answer) }
        & (Join-Path $app 'Configure-GroupAssistant.ps1')
        $savedSetup = Read-OperatorSetup $saved $base
        Assert-Check (-not $savedSetup.Groups[0].Members[0].Enabled -and $script:answers.Count -eq 0) '终端停用/保存/退出流程失败。'
        # Login must honor the saved executable/home. Poison only the disposable
        # base executable so regressions cannot accidentally launch real Codex.
        $loginBase = Get-OperatorBase $app
        $loginBase.CodexCommand = Join-Path $app 'NEVER-RUN.exe'
        Write-PrivateJson (Join-Path $app 'config.group-assistant.json') $loginBase
        $savedSetup.Paths.CodexCommand = $fixtureBinary
        $savedSetup.Paths.CodexHome = Join-Path $app 'runtime\selected-codex-home'
        Write-PrivateJson $saved $savedSetup
        & (Join-Path $app 'Login-Codex.ps1') -StatusOnly
        Assert-Check (Test-Path -LiteralPath (Join-Path $savedSetup.Paths.CodexHome 'fixture-login-marker.txt')) '登录入口没有沿用保存的 Codex 路径和专用目录。'
        Assert-Check (@(Get-ChildItem -LiteralPath (Join-Path $app 'runtime') -Filter '.login-check-*').Count -eq 0) '登录检查临时文件未清理。'
        $lock = [IO.File]::Open((Join-Path $app 'runtime\operator.lock'),'Open','ReadWrite','None')
        try { Assert-Rejected { & (Join-Path $app 'Start-GroupAssistant.ps1') } }
        finally { $lock.Dispose() }
    } finally {
        foreach ($key in $previous.Keys) { [Environment]::SetEnvironmentVariable($key,$previous[$key],'Process') }
    }
    Write-Output 'PASS: first two-choice picker; atomic path persistence; next startup silent; repeated invalidation/recovery; probe failure never saves/starts; stale-version/service-lock protection; direct formal/config/member fixtures. No real login or connection.'
} finally {
    # Only this test-created directory is removed.
    $resolved = (Resolve-Path -LiteralPath $taskDirectory).Path
    $expectedParent = (Resolve-Path -LiteralPath (Join-Path $ProjectRoot 'validation')).Path
    if ([IO.Path]::GetDirectoryName($resolved) -ne $expectedParent -or -not [IO.Path]::GetFileName($resolved).StartsWith('operator-fixture-')) { throw 'Unexpected fixture cleanup path.' }
    Remove-Item -LiteralPath $resolved -Recurse
}
