[CmdletBinding()]
param([switch]$Apply)
$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.IO.Compression
Add-Type -AssemblyName System.IO.Compression.FileSystem
$root = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..')).TrimEnd('\')
if (-not (Test-Path -LiteralPath (Join-Path $root 'go.mod'))) { throw 'Not a MeshMux workspace' }
$targets = @('build', 'release') | ForEach-Object { [IO.Path]::GetFullPath((Join-Path $root $_)) }
foreach ($target in $targets) {
    if ([IO.Path]::GetDirectoryName($target) -ne $root) { throw "Target escaped workspace: $target" }
}
$existing = @($targets | Where-Object { Test-Path -LiteralPath $_ })
if ($existing.Count -eq 0) { Write-Output 'No local build or release outputs to archive.'; return }
$entries = @($existing | ForEach-Object { Get-Item -LiteralPath $_; Get-ChildItem -LiteralPath $_ -Force -Recurse })
if (@($entries | Where-Object { $_.Attributes -band [IO.FileAttributes]::ReparsePoint }).Count) { throw 'Reparse points require manual review' }
$files = @($entries | Where-Object { -not $_.PSIsContainer })
$active = @(Get-CimInstance Win32_Process | Where-Object {
    $path = $_.ExecutablePath
    $path -and @($existing | Where-Object { $path.StartsWith($_ + '\', [StringComparison]::OrdinalIgnoreCase) }).Count
})
if ($active.Count) { throw 'A process is running from a target directory; stop it explicitly before archiving' }
$records = @($files | ForEach-Object {
    [PSCustomObject]@{ path = $_.FullName.Substring($root.Length + 1).Replace('\','/'); bytes = $_.Length; sha256 = (Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256).Hash }
})
$records | Group-Object { $_.path.Split('/')[0] } | ForEach-Object {
    Write-Output ("{0}: {1} files, {2} bytes" -f $_.Name, $_.Count, ($_.Group | Measure-Object bytes -Sum).Sum)
}
if (-not $Apply) { Write-Output 'Preview only. Use -Apply to verify an archive and remove these exact output directories.'; return }
$archiveRoot = Join-Path $root '.local-history'
if (Test-Path -LiteralPath $archiveRoot) {
    if ((Get-Item -LiteralPath $archiveRoot).Attributes -band [IO.FileAttributes]::ReparsePoint) { throw 'Archive root must not be a reparse point' }
}
$archiveDir = Join-Path $archiveRoot ((Get-Date -Format 'yyyyMMdd-HHmmss') + '-' + [guid]::NewGuid().ToString('N').Substring(0,8))
New-Item -ItemType Directory -Path $archiveDir | Out-Null
$archivePath = Join-Path $archiveDir 'obsolete-builds.zip'
$zip = [IO.Compression.ZipFile]::Open($archivePath, [IO.Compression.ZipArchiveMode]::Create)
try {
    foreach ($record in $records) {
        $source = Join-Path $root $record.path
        [IO.Compression.ZipFileExtensions]::CreateEntryFromFile($zip, $source, $record.path, [IO.Compression.CompressionLevel]::Optimal) | Out-Null
    }
} finally { $zip.Dispose() }
$zip = [IO.Compression.ZipFile]::OpenRead($archivePath)
try {
    if ($zip.Entries.Count -ne $records.Count) { throw 'Archive entry count mismatch' }
    foreach ($record in $records) {
        $entry = $zip.GetEntry($record.path)
        if (-not $entry -or $entry.Length -ne $record.bytes) { throw "Archive size mismatch: $($record.path)" }
        $stream = $entry.Open()
        $sha = [Security.Cryptography.SHA256]::Create()
        try { $hash = [BitConverter]::ToString($sha.ComputeHash($stream)).Replace('-','') }
        finally { $sha.Dispose(); $stream.Dispose() }
        if ($hash -ne $record.sha256) { throw "Archive hash mismatch: $($record.path)" }
    }
} finally { $zip.Dispose() }
$archiveHash = (Get-FileHash -LiteralPath $archivePath -Algorithm SHA256).Hash
[PSCustomObject]@{ status = 'verified-before-removal'; archive = 'obsolete-builds.zip'; sha256 = $archiveHash; files = $records } |
    ConvertTo-Json -Depth 5 | Set-Content -LiteralPath (Join-Path $archiveDir 'manifest.json') -Encoding UTF8
$latest = @($existing | ForEach-Object { Get-ChildItem -LiteralPath $_ -Recurse -Force })
if (@($latest | Where-Object { $_.Attributes -band [IO.FileAttributes]::ReparsePoint }).Count) { throw 'Reparse point appeared after archive verification' }
if (@($latest | Where-Object { -not $_.PSIsContainer }).Count -ne $records.Count) { throw 'File set changed during archiving' }
foreach ($record in $records) {
    if ((Get-FileHash -LiteralPath (Join-Path $root $record.path) -Algorithm SHA256).Hash -ne $record.sha256) { throw "Source changed: $($record.path)" }
}
foreach ($target in $existing) {
    $resolved = (Resolve-Path -LiteralPath $target).ProviderPath.TrimEnd('\')
    if ($resolved -notin $targets -or [IO.Path]::GetDirectoryName($resolved) -ne $root) { throw "Unchecked deletion target: $resolved" }
    Remove-Item -LiteralPath $resolved -Recurse -Force
    if (Test-Path -LiteralPath $resolved) { throw "Target still exists: $resolved" }
}
Write-Output "Verified and archived $($records.Count) files: $archivePath"
Write-Output "SHA256: $archiveHash"
