param([Parameter(Mandatory)][string]$Target, [Parameter(Mandatory)][string]$Destination)
$ErrorActionPreference = 'Stop'
if (-not $env:FFMPEG_BUNDLE_ROOT) {
    Write-Warning 'FFmpeg is not bundled. Users must install FFmpeg with libmp3lame separately for MP3 generation.'
    return
}
$source = Join-Path $env:FFMPEG_BUNDLE_ROOT $Target
$name = if ($Target.StartsWith('windows-')) { 'ffmpeg.exe' } else { 'ffmpeg' }
$binary = Join-Path $source $name
$expected = Get-Content -LiteralPath (Join-Path $source 'SHA256SUMS.txt') | Where-Object { $_ -match "^[a-fA-F0-9]{64}\s+\*?$([regex]::Escape($name))$" }
if (@($expected).Count -ne 1) { throw "Missing or ambiguous checksum for $Target/$name." }
$hash = (Get-FileHash -Algorithm SHA256 -LiteralPath $binary).Hash
if ($hash -ine ($expected -split '\s+')[0]) { throw 'FFmpeg checksum mismatch.' }
$licenses = Join-Path $source 'LICENSES'
if (-not (Test-Path -LiteralPath $licenses -PathType Container) -or @(Get-ChildItem -LiteralPath $licenses -File -Recurse).Count -eq 0) { throw 'FFmpeg license and source notices are required.' }
Copy-Item -LiteralPath $binary -Destination $Destination
Copy-Item -LiteralPath $licenses -Destination (Join-Path $Destination 'FFMPEG-LICENSES') -Recurse
