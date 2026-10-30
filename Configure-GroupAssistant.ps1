$ErrorActionPreference = 'Stop'
# Explicit settings-only entry. It never starts an office connection.
& (Join-Path $PSScriptRoot 'Start-GroupAssistant.ps1') -Configure
