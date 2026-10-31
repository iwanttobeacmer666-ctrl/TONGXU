# Shared installed/development layout. No directory is created or migrated here.
function Test-InstalledLayout([string]$ProjectRoot) {
    $marker = Join-Path $ProjectRoot 'installed-layout.json'
    if (-not (Test-Path -LiteralPath $marker -PathType Leaf)) { return $false }
    if ((Get-Item -LiteralPath $marker).Length -gt 1024) { throw 'Invalid installed layout.' }
    $value = Get-Content -LiteralPath $marker -Raw -Encoding UTF8 | ConvertFrom-Json
    if ($value.Version -ne 1) { throw 'Unsupported installed layout.' }
    return $true
}
function Get-OperatorRuntime([string]$ProjectRoot) {
    if (Test-InstalledLayout $ProjectRoot) {
        return Join-Path ([Environment]::GetFolderPath('LocalApplicationData')) 'Tongxu\runtime'
    }
    return Join-Path $ProjectRoot 'runtime'
}
