$ErrorActionPreference = 'Stop'
# Compatibility entry: the same direct formal startup and process lock.
& (Join-Path $PSScriptRoot 'Start-GroupAssistant.ps1')
