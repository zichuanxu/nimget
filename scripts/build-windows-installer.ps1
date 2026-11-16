#Requires -Version 7.4
[CmdletBinding()]
param(
    [Parameter(Mandatory)]
    [ValidatePattern('^v(0|[1-9][0-9]{0,4})\.(0|[1-9][0-9]{0,4})\.(0|[1-9][0-9]{0,4})(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$')]
    [string]$Version,
    [string]$OutputDirectory = 'dist/windows',
    [string]$MakeNSIS = 'makensis.exe'
)

$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $true
if (!$IsWindows) { throw 'Build the Windows installer on Windows.' }
$numericVersion = ($Version.Substring(1) -split '-', 2)[0]
if ($numericVersion.Split('.') | Where-Object { [int]$_ -gt 65535 }) {
    throw 'Each numeric version field must fit in a Windows version resource (0–65535).'
}

$compiler = (Get-Command $MakeNSIS -CommandType Application).Source
$compilerVersion = & $compiler /VERSION
if ($compilerVersion -ne 'v3.13') { throw "Expected NSIS v3.13, found $compilerVersion." }
$repo = Split-Path -Parent $PSScriptRoot
$sourceCommit = git -C $repo rev-parse HEAD
$app = Join-Path $repo 'app'
$resourceDir = Join-Path $app 'build/windows'
$output = [IO.Path]::GetFullPath($OutputDirectory, $repo)
$stage = (New-Item -ItemType Directory -Path (Join-Path $app ('bin/nimget-build-' + [Guid]::NewGuid()))).FullName
$infoPath = Join-Path $stage 'info.json'
$syso = Join-Path $stage 'rsrc_windows_amd64.syso'
$packageResource = Join-Path $app 'rsrc_windows_amd64.syso'
$binary = Join-Path $stage 'nimget.exe'
$setupName = "NimGet-$Version-windows-x64-setup.exe"
$setup = Join-Path $stage $setupName
$icon = Join-Path $resourceDir 'icon.ico'

try {
    Push-Location (Join-Path $app 'frontend')
    try {
        npm ci
        npm run build
    } finally { Pop-Location }

    $info = Get-Content -LiteralPath (Join-Path $resourceDir 'info.json') -Raw | ConvertFrom-Json -AsHashtable
    $info.fixed.file_version = "$numericVersion.0"
    $info.fixed.product_version = "$numericVersion.0"
    $info.info['0000'].FileVersion = $numericVersion
    $info.info['0000'].ProductVersion = $Version
    $info | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath $infoPath -Encoding utf8

    Push-Location $app
    try {
        $wailsVersion = go list -m -f '{{.Version}}' github.com/wailsapp/wails/v3
        $resources = @('-arch', 'amd64', '-icon', $icon, '-manifest', (Join-Path $resourceDir 'wails.exe.manifest'),
            '-info', $infoPath, '-out', $syso)
        go run "github.com/wailsapp/wails/v3/cmd/wails3@$wailsVersion" generate syso @resources
        $targets = @{ GOOS = 'windows'; GOARCH = 'amd64'; CGO_ENABLED = '0' }
        $original = @{}
        # Go links resource objects from disk. Move the generated object onto
        # the same volume without overwriting any other build's resource.
        [IO.File]::Move($syso, $packageResource)
        try {
            foreach ($name in $targets.Keys) {
                $original[$name] = [Environment]::GetEnvironmentVariable($name)
                [Environment]::SetEnvironmentVariable($name, $targets[$name])
            }
            $flags = "-s -w -H windowsgui -X main.version=$Version"
            go build -tags production -trimpath -ldflags $flags -o $binary .
        } finally {
            foreach ($name in $original.Keys) { [Environment]::SetEnvironmentVariable($name, $original[$name]) }
            Remove-Item -LiteralPath $packageResource -Force
        }
    } finally { Pop-Location }

    $definitions = @("/DVERSION=$Version", "/DNUMVERSION=$numericVersion", "/DSOURCECOMMIT=$sourceCommit", "/DBINARY=$binary",
        "/DLICENSE=$(Join-Path $repo 'LICENSE')", "/DNOTICE=$(Join-Path $repo 'NOTICE')",
        "/DICON=$icon", "/DOUTFILE=$setup")
    & $compiler /V2 /WX @definitions (Join-Path $resourceDir 'installer.nsi')
    New-Item -ItemType Directory -Path $output -Force | Out-Null
    Move-Item -LiteralPath $setup -Destination (Join-Path $output $setupName) -Force
    Write-Host (Join-Path $output $setupName)
} finally {
    Remove-Item -LiteralPath $infoPath,$syso,$binary,$setup -Force -ErrorAction SilentlyContinue
    Remove-Item -LiteralPath $stage -Force
}
