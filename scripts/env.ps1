# Dot-source: . .\scripts\env.ps1
param([string]$Path = (Join-Path $PSScriptRoot '..\.env'))
$ErrorActionPreference = 'Stop'
foreach ($line in Get-Content -LiteralPath $Path) {
    if ([string]::IsNullOrWhiteSpace($line) -or $line.TrimStart().StartsWith('#')) { continue }
    if ($line -notmatch '^([A-Z][A-Z0-9_]*)=(.*)$') { throw 'Invalid .env line; expected KEY=value' }
    # Values are literal: this loader never evaluates shell expressions.
    [Environment]::SetEnvironmentVariable($Matches[1], $Matches[2], 'Process')
}
