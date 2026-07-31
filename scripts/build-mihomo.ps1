param(
    [string]$SourceDir,
    [string]$OutputDir = "build\mihomo",
    [switch]$SkipTests,
    [switch]$PackageSource
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$upstreamRepo = "https://github.com/MetaCubeX/mihomo.git"
$upstreamCommit = "fc8c5a24b16991f98cd736950c17d1aa306a5041"
$upstreamVersion = "v1.19.26"
$patchVersion = "meshmux.1"
$version = "$upstreamVersion-$patchVersion"
$patchPath = Join-Path $repoRoot "third_party\mihomo\tailnet-inbound-forwards.patch"
$outputRoot = if ([System.IO.Path]::IsPathRooted($OutputDir)) {
    [System.IO.Path]::GetFullPath($OutputDir)
} else {
    [System.IO.Path]::GetFullPath((Join-Path $repoRoot $OutputDir))
}

if (-not $SourceDir) {
    $SourceDir = Join-Path $env:TEMP "meshmux-mihomo-$upstreamCommit"
}
$sourceRoot = [System.IO.Path]::GetFullPath($SourceDir)
if ([string]::Equals($sourceRoot, $repoRoot, [System.StringComparison]::OrdinalIgnoreCase)) {
    throw "SourceDir must not be the MeshMux repository"
}

if (Test-Path -LiteralPath $sourceRoot -PathType Container) {
    if (-not (Test-Path -LiteralPath (Join-Path $sourceRoot ".git"))) {
        throw "SourceDir exists but is not a Git repository: $sourceRoot"
    }
    $origin = (git -C $sourceRoot remote get-url origin).Trim()
    if ($LASTEXITCODE -ne 0) {
        throw "Unable to read mihomo origin"
    }
    $originNormalized = $origin.TrimEnd("/")
    $upstreamNormalized = $upstreamRepo.TrimEnd("/")
    if ($originNormalized.EndsWith(".git", [System.StringComparison]::OrdinalIgnoreCase)) {
        $originNormalized = $originNormalized.Substring(0, $originNormalized.Length - 4)
    }
    if ($upstreamNormalized.EndsWith(".git", [System.StringComparison]::OrdinalIgnoreCase)) {
        $upstreamNormalized = $upstreamNormalized.Substring(0, $upstreamNormalized.Length - 4)
    }
    if (-not [string]::Equals($originNormalized, $upstreamNormalized, [System.StringComparison]::OrdinalIgnoreCase)) {
        throw "SourceDir origin is not the pinned mihomo upstream: $origin"
    }
} else {
    git clone $upstreamRepo $sourceRoot
    if ($LASTEXITCODE -ne 0) {
        throw "Unable to clone mihomo"
    }
}

git -C $sourceRoot fetch origin $upstreamCommit --depth 1
if ($LASTEXITCODE -ne 0) { throw "Unable to fetch pinned mihomo commit" }
git -C $sourceRoot reset --hard $upstreamCommit
if ($LASTEXITCODE -ne 0) { throw "Unable to reset mihomo to pinned commit" }
git -C $sourceRoot clean -fdx
if ($LASTEXITCODE -ne 0) { throw "Unable to clean mihomo worktree" }

$actualCommit = (git -C $sourceRoot rev-parse HEAD).Trim()
if ($LASTEXITCODE -ne 0) { throw "Unable to read mihomo commit" }
if ($actualCommit -ne $upstreamCommit) {
    throw "mihomo commit mismatch: $actualCommit"
}

git -C $sourceRoot apply --check $patchPath
if ($LASTEXITCODE -ne 0) { throw "Mihomo patch check failed" }
git -C $sourceRoot apply $patchPath
if ($LASTEXITCODE -ne 0) { throw "Mihomo patch apply failed" }

if (-not $SkipTests) {
    Push-Location $sourceRoot
    try {
        go test -tags with_gvisor ./...
        if ($LASTEXITCODE -ne 0) { throw "Mihomo tests failed" }
    } finally {
        Pop-Location
    }
}

New-Item -ItemType Directory -Force -Path $outputRoot | Out-Null
$buildTime = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
$exeName = "mihomo-windows-amd64-compatible.exe"
$exePath = Join-Path $outputRoot $exeName
$ldflags = @(
    "-X", "github.com/metacubex/mihomo/constant.Version=$version",
    "-X", "github.com/metacubex/mihomo/constant.BuildTime=$buildTime",
    "-w", "-s", "-buildid="
) -join " "

Push-Location $sourceRoot
$oldCGOEnabled = $env:CGO_ENABLED
$oldGOOS = $env:GOOS
$oldGOARCH = $env:GOARCH
$oldGOAMD64 = $env:GOAMD64
try {
    $env:CGO_ENABLED = "0"
    $env:GOOS = "windows"
    $env:GOARCH = "amd64"
    $env:GOAMD64 = "v1"
    go build -tags with_gvisor -trimpath -ldflags $ldflags -o $exePath .
    if ($LASTEXITCODE -ne 0) { throw "Mihomo build failed" }
} finally {
    $env:CGO_ENABLED = $oldCGOEnabled
    $env:GOOS = $oldGOOS
    $env:GOARCH = $oldGOARCH
    $env:GOAMD64 = $oldGOAMD64
    Pop-Location
}

$binaryArchive = Join-Path $outputRoot "mihomo-windows-amd64-compatible-$version.zip"
if (Test-Path -LiteralPath $binaryArchive) {
    Remove-Item -LiteralPath $binaryArchive -Force
}
Compress-Archive -LiteralPath $exePath -DestinationPath $binaryArchive -CompressionLevel Optimal

if ($PackageSource) {
    $sourceArchive = Join-Path $outputRoot "mihomo-source-$version.zip"
    if (Test-Path -LiteralPath $sourceArchive) {
        Remove-Item -LiteralPath $sourceArchive -Force
    }
    $sourceStage = Join-Path $outputRoot "source-$version"
    if (Test-Path -LiteralPath $sourceStage) {
        Remove-Item -LiteralPath $sourceStage -Recurse -Force
    }
    New-Item -ItemType Directory -Force -Path $sourceStage | Out-Null
    git -C $sourceRoot add -A
    if ($LASTEXITCODE -ne 0) { throw "Unable to stage patched mihomo source" }
    $sourceTree = (git -C $sourceRoot write-tree).Trim()
    if ($LASTEXITCODE -ne 0) { throw "Unable to create patched mihomo source tree" }
    $sourceTar = Join-Path $outputRoot "mihomo-source-$version.tar"
    if (Test-Path -LiteralPath $sourceTar) {
        Remove-Item -LiteralPath $sourceTar -Force
    }
    git -C $sourceRoot archive --format=tar --output=$sourceTar $sourceTree
    if ($LASTEXITCODE -ne 0) { throw "Unable to archive patched mihomo source" }
    tar -xf $sourceTar -C $sourceStage
    if ($LASTEXITCODE -ne 0) { throw "Unable to extract patched mihomo source" }
    Remove-Item -LiteralPath $sourceTar -Force
    Copy-Item -LiteralPath $patchPath -Destination (Join-Path $sourceStage "MESHMUX_PATCH.patch")
    @"
Upstream: $upstreamRepo
Commit: $upstreamCommit
Version: $version
Patch: MESHMUX_PATCH.patch
Build: CGO_ENABLED=0 GOOS=windows GOARCH=amd64 GOAMD64=v1 go build -tags with_gvisor
"@ | Set-Content -LiteralPath (Join-Path $sourceStage "MESHMUX_BUILD.txt") -Encoding utf8
    Compress-Archive -Path (Join-Path $sourceStage "*") -DestinationPath $sourceArchive -CompressionLevel Optimal
    Remove-Item -LiteralPath $sourceStage -Recurse -Force
}

Write-Output $exePath
Write-Output $binaryArchive
