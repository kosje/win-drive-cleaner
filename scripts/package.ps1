param([string]$OutputDirectory = 'dist/release', [string]$ExpectedVersion = '')
$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot
Set-Location -LiteralPath $repoRoot
$outputPath = [IO.Path]::GetFullPath((Join-Path $repoRoot $OutputDirectory))
$workspacePrefix = $repoRoot.TrimEnd('\') + '\'
if (-not $outputPath.StartsWith($workspacePrefix, [StringComparison]::OrdinalIgnoreCase) -or
    $outputPath.StartsWith((Join-Path $repoRoot '.git'), [StringComparison]::OrdinalIgnoreCase)) {
    throw 'Package output must stay inside the workspace and outside .git'
}
$versionText = [IO.File]::ReadAllText((Join-Path $repoRoot 'main.go'))
$releaseVersion = [regex]::Match($versionText, 'const version = "([0-9]+\.[0-9]+\.[0-9]+)"').Groups[1].Value
if (-not $releaseVersion) { throw 'Release version not found' }
if ($ExpectedVersion -and $ExpectedVersion -ne "v$releaseVersion") { throw 'Tag and source version disagree' }
New-Item -ItemType Directory -Path $outputPath -Force | Out-Null
$oldGoArch = $env:GOARCH
$oldGoOS = $env:GOOS
$oldCgo = $env:CGO_ENABLED
try {
    $env:GOOS = 'windows'
    $env:CGO_ENABLED = '0'
    foreach ($arch in @('amd64', '386')) {
        $env:GOARCH = $arch
        $name = if ($arch -eq 'amd64') { 'WinDriveCleaner-x64.exe' } else { 'WinDriveCleaner-x86.exe' }
        $exePath = Join-Path $outputPath $name
        & go build -buildvcs=false -trimpath -ldflags '-s -w' -o $exePath .
        if ($LASTEXITCODE -ne 0) { throw "Build failed: $arch" }
        $reportedVersion = & $exePath --version
        if ($LASTEXITCODE -ne 0 -or $reportedVersion -notmatch "v$([regex]::Escape($releaseVersion))$") {
            throw "Version verification failed: $name"
        }
    }
} finally {
    $env:GOARCH = $oldGoArch
    $env:GOOS = $oldGoOS
    $env:CGO_ENABLED = $oldCgo
}
Add-Type -AssemblyName System.IO.Compression, System.IO.Compression.FileSystem
$zipName = "WinDriveCleaner-v$releaseVersion.zip"
$zipPath = Join-Path $outputPath $zipName
$zipStream = [IO.File]::Open($zipPath, [IO.FileMode]::Create)
$zip = New-Object IO.Compression.ZipArchive($zipStream, [IO.Compression.ZipArchiveMode]::Create)
$utf8 = New-Object Text.UTF8Encoding($false)
try {
    foreach ($name in @('WinDriveCleaner-x64.exe', 'WinDriveCleaner-x86.exe', 'README.md', 'LICENSE', 'REVIEW.md', 'CACHE_RULES.md')) {
        $entry = $zip.CreateEntry($name, [IO.Compression.CompressionLevel]::Optimal)
        $entry.LastWriteTime = [DateTimeOffset]::new(2000, 1, 1, 0, 0, 0, [TimeSpan]::Zero)
        $stream = $entry.Open()
        try {
            if ($name.EndsWith('.exe')) {
                $inputStream = [IO.File]::OpenRead((Join-Path $outputPath $name))
                try { $inputStream.CopyTo($stream) } finally { $inputStream.Dispose() }
            } else {
                $document = [IO.File]::ReadAllText((Join-Path $repoRoot $name)).Replace("`r`n", "`n")
                $bytes = $utf8.GetBytes($document)
                $stream.Write($bytes, 0, $bytes.Length)
            }
        } finally { $stream.Dispose() }
    }
} finally { $zip.Dispose(); $zipStream.Dispose() }
$names = @('WinDriveCleaner-x64.exe', 'WinDriveCleaner-x86.exe', $zipName)
$sums = foreach ($name in $names) {
    $hash = (Get-FileHash -LiteralPath (Join-Path $outputPath $name) -Algorithm SHA256).Hash.ToLowerInvariant()
    "$hash  $name"
}
[IO.File]::WriteAllText((Join-Path $outputPath 'SHA256SUMS'), ($sums -join "`n") + "`n", $utf8)
Write-Output "Package ready: $outputPath"
