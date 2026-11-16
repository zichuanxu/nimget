#Requires -Version 7.4
[CmdletBinding()]
param(
    [Parameter(Mandatory)]
    [ValidatePattern('\A(?-i:v)(0|[1-9][0-9]{0,4})\.(0|[1-9][0-9]{0,4})\.(0|[1-9][0-9]{0,4})(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?\z')]
    [string]$Version,
    [string]$OutputDirectory = 'dist/release',
    [string]$MakeNSIS = 'makensis.exe',
    [switch]$VerifyOnly
)

$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $true
if (!$IsWindows) { throw 'Prepare and verify the Windows release on Windows.' }

$numericVersion = ($Version.Substring(1) -split '-', 2)[0]
if ($numericVersion -eq '0.0.0' -or ($numericVersion.Split('.') | Where-Object { [int]$_ -gt 65535 })) {
    throw 'Use a nonzero version with each numeric field in the range 0–65535.'
}

$repo = Split-Path -Parent $PSScriptRoot
$sourceCommit = git -C $repo rev-parse HEAD
$bundle = Join-Path ([IO.Path]::GetFullPath($OutputDirectory, $repo)) $Version
$installerName = "NimGet-$Version-windows-x64-setup.exe"
$archiveName = "NimGet-$Version-chromium-extension.zip"
$metadataName = "NimGet-$Version-release.json"
$checksumName = 'SHA256SUMS.txt'
$assetNames = @($archiveName, $metadataName, $installerName)
$expectedFiles = @($assetNames) + $checksumName

$sourcePaths = git -C $repo ls-tree -r --name-only $sourceCommit -- LICENSE NOTICE extension
$archivePaths = @($sourcePaths -creplace '^extension/', '')
$manifest = git -C $repo show "${sourceCommit}:extension/manifest.json" | ConvertFrom-Json -AsHashtable
$manifest.version = $numericVersion
$manifest.version_name = $Version
$manifestJson = $manifest | ConvertTo-Json -Depth 16

function Get-ReleaseChecksums([string]$Directory) {
    foreach ($name in $assetNames) {
        $hash = Get-FileHash -Algorithm SHA256 -LiteralPath (Join-Path $Directory $name)
        "$($hash.Hash.ToLowerInvariant())  $name"
    }
}

function Assert-ReleaseBundle([string]$Directory) {
    $files = @(Get-ChildItem -LiteralPath $Directory -Force)
    if ($files.Count -ne $expectedFiles.Count -or ($files | Where-Object {
        $_.PSIsContainer -or ($_.Attributes -band [IO.FileAttributes]::ReparsePoint) -or $_.Name -cnotin $expectedFiles
    })) {
        throw "Unexpected release contents in $Directory"
    }
    $checksums = Get-Content -LiteralPath (Join-Path $Directory $checksumName)
    if (($checksums -join "`n") -cne ((Get-ReleaseChecksums $Directory) -join "`n")) {
        throw 'SHA-256 mismatch in the release bundle.'
    }

    $metadata = Get-Content -LiteralPath (Join-Path $Directory $metadataName) -Raw | ConvertFrom-Json
    if ($metadata.version -cne $Version -or $metadata.sourceCommit -cne $sourceCommit -or
        $metadata.windows.installer -cne $installerName -or $metadata.windows.architecture -cne 'x64' -or
        $metadata.windows.signed -ne $false -or $metadata.extension.archive -cne $archiveName -or
        $metadata.extension.version -cne $numericVersion) {
        throw 'Release metadata does not match the requested version, source commit, or assets.'
    }
    $installer = Join-Path $Directory $installerName
    $info = [Diagnostics.FileVersionInfo]::GetVersionInfo($installer)
    $fileVersion = "$($info.FileMajorPart).$($info.FileMinorPart).$($info.FileBuildPart)"
    if ($info.ProductName -cne 'NimGet' -or $info.ProductVersion -cne $Version -or $fileVersion -cne $numericVersion) {
        throw 'Windows setup version resources do not match the release version.'
    }
    if ((Get-AuthenticodeSignature -LiteralPath $installer).Status -ne 'NotSigned') {
        throw 'Unexpected code-signing status for Windows setup.'
    }

    $archive = [IO.Compression.ZipFile]::OpenRead((Join-Path $Directory $archiveName))
    try {
        if (($archive.Entries.FullName -join "`n") -cne ($archivePaths -join "`n")) {
            throw 'Unexpected extension archive contents.'
        }
        $reader = [IO.StreamReader]::new($archive.GetEntry('manifest.json').Open())
        try { $archiveManifest = $reader.ReadToEnd() } finally { $reader.Dispose() }
        if ($archiveManifest -cne ($manifestJson -replace "`r`n", "`n")) {
            throw 'Extension manifest does not match the release version and source manifest.'
        }
    } finally { $archive.Dispose() }
    Write-Host "Verified $Version from $sourceCommit in $Directory"
}

if ($VerifyOnly) {
    Assert-ReleaseBundle $bundle
    return
}
if (Test-Path -LiteralPath $bundle) { throw "Release bundle already exists: $bundle" }
if (git -C $repo status --porcelain --untracked-files=all) { throw 'Prepare a release from a clean checkout.' }

# Stage beside the final directory so the verified bundle can be moved as a unit.
$stage = (New-Item -ItemType Directory -Path (Join-Path (Split-Path $bundle) ('.staging-' + [Guid]::NewGuid()))).FullName
try {
    & (Join-Path $PSScriptRoot 'build-windows-installer.ps1') -Version $Version -OutputDirectory $stage -MakeNSIS $MakeNSIS

    $sourcePath = Join-Path $stage 'source.zip'
    git -C $repo archive --format=zip -o $sourcePath $sourceCommit -- LICENSE NOTICE extension
    $source = [IO.Compression.ZipFile]::OpenRead($sourcePath)
    $archive = $null
    try {
        $archive = [IO.Compression.ZipFile]::Open((Join-Path $stage $archiveName), 'Create')
        foreach ($entry in $source.Entries) {
            if (!$entry.Name) { continue }
            $name = $entry.FullName -creplace '^extension/', ''
            $outputEntry = $archive.CreateEntry($name)
            $outputEntry.LastWriteTime = [DateTimeOffset]::new(1980, 1, 1, 0, 0, 0, [TimeSpan]::Zero)
            $data = $outputEntry.Open()
            try {
                if ($name -ceq 'manifest.json') {
                    $bytes = [Text.Encoding]::UTF8.GetBytes(($manifestJson -replace "`r`n", "`n"))
                    $data.Write($bytes, 0, $bytes.Length)
                } else {
                    $inputData = $entry.Open()
                    try { $inputData.CopyTo($data) } finally { $inputData.Dispose() }
                }
            } finally { $data.Dispose() }
        }
    } finally {
        if ($archive) { $archive.Dispose() }
        $source.Dispose()
    }
    Remove-Item -LiteralPath $sourcePath

    $metadata = [ordered]@{
        version = $Version
        sourceCommit = $sourceCommit
        windows = [ordered]@{ installer = $installerName; architecture = 'x64'; signed = $false }
        extension = [ordered]@{ archive = $archiveName; version = $numericVersion }
        toolchain = [ordered]@{
            go = (go -C (Join-Path $repo 'app') env GOVERSION)
            node = (node --version)
            npm = (npm --version)
            nsis = '3.13'
            powershell = $PSVersionTable.PSVersion.ToString()
        }
    }
    $metadataJson = ($metadata | ConvertTo-Json -Depth 5) -replace "`r`n", "`n"
    [IO.File]::WriteAllText((Join-Path $stage $metadataName), "$metadataJson`n")
    $checksums = (Get-ReleaseChecksums $stage) -join "`n"
    [IO.File]::WriteAllText((Join-Path $stage $checksumName), "$checksums`n")

    Assert-ReleaseBundle $stage
    [IO.Directory]::Move($stage, $bundle)
    Write-Host "Prepared release bundle: $bundle"
} finally {
    if (Test-Path -LiteralPath $stage) {
        Get-ChildItem -LiteralPath $stage -File -Force | Remove-Item -Force
        [IO.Directory]::Delete($stage)
    }
}
