param([string]$ProjectRoot = (Split-Path $PSScriptRoot -Parent))
$ErrorActionPreference='Stop'
$taskFixture=Join-Path $PSScriptRoot ('management-fixture-'+[guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path (Join-Path $taskFixture 'bin'),(Join-Path $taskFixture 'runtime') | Out-Null
try {
    foreach($name in @('Manage-GroupAssistant.ps1','Operator-Setup.ps1','Operator-Paths.ps1','Runtime-Paths.ps1')) {
        Copy-Item -LiteralPath (Join-Path $ProjectRoot $name) -Destination (Join-Path $taskFixture $name)
    }
    $taskExe=Join-Path $taskFixture 'bin\group-assistant.exe'
    Copy-Item -LiteralPath (Join-Path $ProjectRoot 'bin\group-assistant.exe') -Destination $taskExe
    $taskSettings=Join-Path $taskFixture 'runtime\operator-setup.json'
    $taskValue=@{ Version=1;Owner='owner';BotID='SYNTHETIC-BOT-NOT-FOR-DISPLAY';ProtectedSecret='SYNTHETIC-SECRET-NOT-FOR-DISPLAY';Groups=@(@{ID='group';Enabled=$true;Members=@()}) }
    [IO.File]::WriteAllText($taskSettings,($taskValue|ConvertTo-Json -Depth 10),[Text.UTF8Encoding]::new($false))
    $taskAnswers=[Collections.Generic.Queue[string]]::new()
    foreach($answer in @('2','1','成员甲','YES','4','1','成员甲','YES','3','1','成员甲','YES','0')){$taskAnswers.Enqueue($answer)}
    function Read-Host([string]$Prompt) {
        if($taskAnswers.Count -eq 0){throw "Missing management answer: $Prompt"}
        return $taskAnswers.Dequeue()
    }
    $taskOutput=Join-Path $taskFixture 'window-output.txt'
    # An idle launcher has its own lifetime lock; it must not prevent offline
    # management. Short settings locks still serialize actual file updates.
    $taskLauncher=[IO.File]::Open((Join-Path $taskFixture 'runtime\launcher.lock'),'OpenOrCreate','ReadWrite','None')
    try { & (Join-Path $taskFixture 'Manage-GroupAssistant.ps1') *> $taskOutput }
    finally { $taskLauncher.Dispose() }
    if($taskAnswers.Count -ne 0){throw 'Management window did not finish all operations.'}
    $taskSaved=Get-Content -LiteralPath $taskSettings -Raw -Encoding UTF8|ConvertFrom-Json
    if(@($taskSaved.Groups[0].Members).Count -ne 1 -or $taskSaved.Groups[0].Members[0].ID -cne '成员甲' -or -not $taskSaved.Groups[0].Members[0].Enabled){Get-Content -LiteralPath $taskOutput -Tail 40 | Write-Output;throw 'Offline Unicode add/disable/enable did not persist.'}
    if($taskSaved.ProtectedSecret -cne $taskValue.ProtectedSecret){throw 'Private settings changed.'}
    $taskText=Get-Content -LiteralPath $taskOutput -Raw
    if($taskText.Contains($taskValue.ProtectedSecret) -or $taskText.Contains($taskValue.BotID)){throw 'Management window exposed private settings.'}
    $taskBefore=[IO.File]::ReadAllText($taskSettings)
    $taskLock=[IO.File]::Open((Join-Path $taskFixture 'runtime\operator.lock'),'OpenOrCreate','ReadWrite','None')
    try {
        $taskStart=[Diagnostics.ProcessStartInfo]::new()
        $taskStart.FileName=$taskExe
        $taskStart.Arguments='-manage-local -operator-settings "'+$taskSettings+'"'
        $taskStart.UseShellExecute=$false;$taskStart.CreateNoWindow=$true
        $taskStart.RedirectStandardInput=$true;$taskStart.RedirectStandardOutput=$true;$taskStart.RedirectStandardError=$true
        $taskProcess=[Diagnostics.Process]::Start($taskStart)
        try {
            $taskProcess.StandardInput.Write('{"Action":"list"}');$taskProcess.StandardInput.Close()
            $taskOut=$taskProcess.StandardOutput.ReadToEndAsync();$taskErr=$taskProcess.StandardError.ReadToEndAsync()
            $taskProcess.WaitForExit()
            if($taskProcess.ExitCode -eq 0){throw 'Offline mode ignored the running/configuration lock.'}
        }finally{$taskProcess.Dispose()}
    }finally{$taskLock.Dispose()}
    if([IO.File]::ReadAllText($taskSettings) -cne $taskBefore){throw 'Busy offline request changed data.'}
    Write-Output 'PASS: independent management window, offline UTF-8 add/disable/enable persistence, no secret display, and busy-lock refusal. No real service connection.'
}finally {
    $taskResolved=(Resolve-Path -LiteralPath $taskFixture).Path
    if([IO.Path]::GetDirectoryName($taskResolved) -ne (Resolve-Path -LiteralPath $PSScriptRoot).Path -or -not [IO.Path]::GetFileName($taskResolved).StartsWith('management-fixture-')){throw 'Unexpected cleanup path.'}
    Remove-Item -LiteralPath $taskResolved -Recurse
}
