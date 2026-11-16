#Requires -Version 7.4
[CmdletBinding()]
param(
    [Parameter(Mandatory)][string]$Version,
    [string]$OutputDirectory = 'dist/release-test',
    [string]$MakeNSIS = 'makensis.exe'
)

$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $true
$prepare = Join-Path $PSScriptRoot 'prepare-windows-release.ps1'
$repo = Split-Path -Parent $PSScriptRoot
$output = [IO.Path]::GetFullPath($OutputDirectory, $repo)
$bundle = Join-Path $output $Version
$secondOutput = Join-Path $output 'repeat'
$secondBundle = Join-Path $secondOutput $Version

function Assert-Rejected([scriptblock]$Action, [string]$ExpectedError) {
    try { & $Action } catch {
        if ($_ -notlike "*$ExpectedError*") { throw }
        return
    }
    throw "Expected release preparation to reject: $ExpectedError"
}

& $prepare -Version $Version -OutputDirectory $output -MakeNSIS $MakeNSIS
& $prepare -Version $Version -OutputDirectory $secondOutput -MakeNSIS $MakeNSIS
& $prepare -Version $Version -OutputDirectory $output -VerifyOnly

$names = @("NimGet-$Version-windows-x64-setup.exe", "NimGet-$Version-chromium-extension.zip", "NimGet-$Version-release.json", 'SHA256SUMS.txt')
foreach ($name in $names) {
    $first = Get-FileHash -LiteralPath (Join-Path $bundle $name)
    $second = Get-FileHash -LiteralPath (Join-Path $secondBundle $name)
    if ($first.Hash -cne $second.Hash) { throw "Repeated builds differ for $name" }
}

Assert-Rejected { & $prepare -Version $Version -OutputDirectory $output -MakeNSIS $MakeNSIS } 'already exists'
$extra = Join-Path $bundle 'unexpected.txt'
try {
    Set-Content -LiteralPath $extra -Value 'this file does not belong in the bundle'
    Assert-Rejected { & $prepare -Version $Version -OutputDirectory $output -VerifyOnly } 'Unexpected release contents'
} finally { Remove-Item -LiteralPath $extra }

$zip = Join-Path $bundle $names[1]
try {
    [IO.File]::AppendAllText($zip, 'tampered')
    Assert-Rejected { & $prepare -Version $Version -OutputDirectory $output -VerifyOnly } 'SHA-256 mismatch'
} finally { Copy-Item -LiteralPath (Join-Path $secondBundle $names[1]) -Destination $zip -Force }

& $prepare -Version $Version -OutputDirectory $output -VerifyOnly
Write-Host "Release verification and byte-for-byte rebuild passed: $bundle"
