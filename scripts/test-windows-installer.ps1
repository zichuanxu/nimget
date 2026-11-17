#Requires -Version 7.4
# Run against two releases on a disposable Windows test account with WebView2.
[CmdletBinding()]
param(
    [Parameter(Mandatory)][string]$PreviousInstaller,
    [Parameter(Mandatory)][string]$PreviousVersion,
    [Parameter(Mandatory)][string]$Installer,
    [Parameter(Mandatory)][string]$Version
)

$ErrorActionPreference = 'Stop'
$uninstallKey = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\nimget'
$installKey = 'HKCU:\Software\nimget'
$startMenu = Join-Path ([Environment]::GetFolderPath('Programs')) 'NimGet.lnk'
$runKey = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Run'
$service = 'http://127.0.0.1:51000/v1'
$work = Join-Path ([IO.Path]::GetTempPath()) ('nimget-installer-' + [Guid]::NewGuid())
$installDir = Join-Path $work 'Program Files\NimGet'
$alternateInstallDir = Join-Path $work 'Alternate Install\NimGet'
$binary = Join-Path $installDir 'nimget.exe'
$uninstaller = Join-Path $installDir 'uninstall.exe'
$dataDir = Join-Path $work 'AppData\nimget'
$destination = Join-Path $work 'Downloads\installer-smoke.txt'
$payload = 'NimGet installer acceptance payload'

function Assert-Condition($Condition, [string]$Message) {
    if (!$Condition) { throw $Message }
}

function Wait-Condition([scriptblock]$Condition, [string]$Message) {
    $deadline = [DateTime]::UtcNow.AddSeconds(35)
    do {
        if (& $Condition) { return }
        Start-Sleep -Milliseconds 250
    } while ([DateTime]::UtcNow -lt $deadline)
    throw $Message
}

function Run-Setup([string]$Path, [string]$Arguments) {
    $process = Start-Process -FilePath $Path -ArgumentList $Arguments -WindowStyle Hidden -Wait -PassThru
    return $process.ExitCode
}

function Assert-Version([string]$Path, [string]$Expected) {
    $info = [Diagnostics.FileVersionInfo]::GetVersionInfo($Path)
    $numeric = (($Expected -replace '^v', '') -split '-')[0]
    Assert-Condition ($info.ProductVersion -eq $Expected) "$Path product version is $($info.ProductVersion), expected $Expected"
    Assert-Condition ("$($info.FileMajorPart).$($info.FileMinorPart).$($info.FileBuildPart)" -eq $numeric) "$Path has incorrect numeric file version"
}

function Start-InstalledApp {
    $start = [Diagnostics.ProcessStartInfo]::new($binary)
    $start.WorkingDirectory = $installDir
    $start.UseShellExecute = $false
    $start.EnvironmentVariables['APPDATA'] = (Split-Path $dataDir)
    $start.EnvironmentVariables['USERPROFILE'] = $work
    $start.EnvironmentVariables['PATH'] = "$env:SystemRoot\System32;$env:SystemRoot"
    $start.EnvironmentVariables.Remove('FRONTEND_DEVSERVER_URL')
    return [Diagnostics.Process]::Start($start)
}

function Wait-App([Diagnostics.Process]$Process) {
    Wait-Condition {
        $Process.Refresh()
        if ($Process.HasExited) { throw "NimGet exited with code $($Process.ExitCode)" }
        if ($Process.MainWindowTitle -ne 'NimGet' -or !(Test-Path -LiteralPath "$dataDir\token")) { return $false }
        try { $null = Invoke-RestMethod "$service/ping" -TimeoutSec 2; return $true } catch { return $false }
    } 'Installed app did not show a NimGet window and start its embedded service'
}

function Stop-TestApp([Diagnostics.Process]$Process) {
    if ($Process -and !$Process.HasExited) {
        # The lifecycle test leaves tasks queued or complete before stopping
        # the process. Setup and uninstall themselves must never force a quit.
        Stop-Process -Id $Process.Id -Force
        $Process.WaitForExit()
    }
}

$previousPath = (Resolve-Path -LiteralPath $PreviousInstaller).Path
$currentPath = (Resolve-Path -LiteralPath $Installer).Path
Assert-Version $previousPath $PreviousVersion
Assert-Version $currentPath $Version
Assert-Condition (!(Test-Path $uninstallKey) -and !(Test-Path $installKey) -and !(Test-Path -LiteralPath $startMenu)) 'Use an account with no existing NimGet installation'
Assert-Condition (!(Get-ItemProperty -Path $runKey -Name nimget -ErrorAction SilentlyContinue)) 'Use an account without an existing NimGet launch-at-login entry'
Assert-Condition (!(Get-Process nimget -ErrorAction SilentlyContinue)) 'Quit other NimGet processes before running this test'
$listener = [Net.NetworkInformation.IPGlobalProperties]::GetIPGlobalProperties().GetActiveTcpListeners()
Assert-Condition (!(($listener | Where-Object Port -eq 51000))) 'NimGet service port 51000 is busy'

New-Item -ItemType Directory -Path $work, (Split-Path $destination) | Out-Null
Write-Host "Acceptance profile and files: $work"
$portFile = Join-Path $work 'port'
$fixture = Start-Job -ArgumentList $payload, $portFile -ScriptBlock {
    param($Text, $PortFile)
    $server = [Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback, 0)
    $server.Start()
    [IO.File]::WriteAllText($PortFile, $server.LocalEndpoint.Port.ToString())
    while ($true) {
        if (!$server.Pending()) {
            Start-Sleep -Milliseconds 50
            continue
        }
        $client = $server.AcceptTcpClient()
        try {
            $stream = $client.GetStream()
            $stream.ReadTimeout = 5000
            $reader = [IO.StreamReader]::new($stream)
            $request = $reader.ReadLine()
            while ($reader.ReadLine()) { }
            $body = [Text.Encoding]::UTF8.GetBytes($Text)
            $header = [Text.Encoding]::ASCII.GetBytes("HTTP/1.1 200 OK`r`nContent-Length: $($body.Length)`r`nETag: `"nimget-installer`"`r`nConnection: close`r`n`r`n")
            $stream.Write($header, 0, $header.Length)
            if ($request.StartsWith('GET ')) { $stream.Write($body, 0, $body.Length) }
        } finally { $client.Dispose() }
    }
}
$app = $null
try {
    Wait-Condition { Test-Path -LiteralPath $portFile } 'Local download fixture failed to start'
    $port = Get-Content -LiteralPath $portFile
    $setupMutex = [Threading.Mutex]::new($false, 'io.github.amemiya02.nimget.setup')
    try {
        Assert-Condition ((Run-Setup $currentPath "/S /D=$installDir") -eq 5) 'Setup did not refuse a simultaneous install or uninstall'
        Assert-Condition (!(Test-Path $uninstallKey)) 'Setup modified the installation while another operation was in progress'
    } finally { $setupMutex.Dispose() }
    Assert-Condition ((Run-Setup $previousPath "/S /D=$installDir") -eq 0) 'Fresh installation failed'
    Assert-Version $binary $PreviousVersion
    Assert-Condition ((Get-ItemProperty -Path $uninstallKey).DisplayVersion -eq $PreviousVersion) 'Fresh installation has an incorrect Installed Apps version'
    Assert-Condition (Test-Path -LiteralPath $startMenu) 'Start menu shortcut is missing'

    $app = Start-InstalledApp
    Wait-App $app
    $token = (Get-Content -LiteralPath "$dataDir\token" -Raw).Trim()
    $headers = @{ Authorization = "Bearer $token" }
    $settings = (Invoke-WebRequest "$service/settings" -Headers $headers).Content | ConvertFrom-Json -AsHashtable
    $settings.connections = 2
    $settings.desktop.language = 'en'
    $settings.desktop.checkUpdates = $false
    $later = (Get-Date).AddHours(2)
    $settings.schedule = @{ start = $later.ToString('HH:mm'); stop = $later.AddHours(1).ToString('HH:mm') }
    $request = @{ url = "http://127.0.0.1:$port/file.txt"; destination = $destination; priority = 1 }
    $null = Invoke-RestMethod "$service/settings" -Headers $headers -Method Put -ContentType 'application/json' -Body ($settings | ConvertTo-Json -Depth 10)
    $queued = Invoke-RestMethod "$service/downloads" -Headers $headers -Method Post -ContentType 'application/json' -Body ($request | ConvertTo-Json)
    Assert-Condition ($queued.status -eq 'queued') 'Fixture download was not queued before upgrade'

    Assert-Condition ((Run-Setup $currentPath '/S') -eq 2) 'Upgrade did not refuse to replace a running app'
    Assert-Version $binary $PreviousVersion
    Stop-TestApp $app
    Assert-Condition ((Run-Setup $currentPath "/S /D=$alternateInstallDir") -eq 0) 'In-place upgrade failed'
    Assert-Condition (!(Test-Path -LiteralPath $alternateInstallDir)) 'Upgrade wrote to an alternate directory'
    Assert-Version $binary $Version
    Assert-Condition ((Get-ItemProperty -Path $uninstallKey).DisplayVersion -eq $Version) 'Upgrade did not update the Installed Apps version'

    $app = Start-InstalledApp
    Wait-App $app
    $settings = Invoke-RestMethod "$service/settings" -Headers $headers
    Assert-Condition ($settings.connections -eq 2 -and $settings.desktop.language -eq 'en' -and $settings.schedule.start -eq $later.ToString('HH:mm')) 'Upgrade lost settings'
    $item = (Invoke-RestMethod "$service/downloads" -Headers $headers) | Where-Object id -eq $queued.id
    Assert-Condition ($item.status -eq 'queued' -and $item.priority -eq 1 -and $item.destination -eq $destination) 'Upgrade lost the queued task or its options'
    $settings.schedule = @{}
    $null = Invoke-RestMethod "$service/settings" -Headers $headers -Method Put -ContentType 'application/json' -Body ($settings | ConvertTo-Json -Depth 10)
    Wait-Condition {
        ((Invoke-RestMethod "$service/downloads" -Headers $headers) | Where-Object id -eq $queued.id).status -eq 'completed'
    } 'Queued download did not complete after upgrade'
    Assert-Condition ((Get-Content -LiteralPath $destination -Raw) -ceq $payload) 'Downloaded bytes do not match the fixture'

    Assert-Condition ((Run-Setup $uninstaller "/S _?=$installDir") -eq 2) 'Uninstall did not refuse to remove a running app'
    Stop-TestApp $app
    $staleDir = New-Item -ItemType Directory -Path $alternateInstallDir
    $staleUninstaller = Join-Path $staleDir.FullName 'uninstall.exe'
    Copy-Item -LiteralPath $uninstaller -Destination $staleUninstaller
    Assert-Condition ((Run-Setup $staleUninstaller "/S _?=$($staleDir.FullName)") -eq 6) 'Uninstall accepted a directory outside the registered installation'
    Assert-Condition ((Get-ItemProperty -Path $uninstallKey).InstallLocation -eq $installDir) 'Uninstall changed the registered installation from another directory'
    # Simulate the desktop's opt-in launch-at-login entry for this binary.
    if (!(Test-Path $runKey)) { New-Item -Path $runKey | Out-Null }
    Set-ItemProperty -Path $runKey -Name nimget -Value ('"' + $binary + '"')
    Assert-Condition ((Run-Setup $uninstaller '/S') -eq 0) 'Uninstall failed'
    Wait-Condition { !(Test-Path -LiteralPath $binary) } 'Uninstall left the app executable'
    Assert-Condition (!(Test-Path $uninstallKey) -and !(Test-Path $installKey) -and !(Test-Path -LiteralPath $startMenu)) 'Uninstall left the Installed Apps entry or Start menu shortcut'
    Assert-Condition (!(Get-ItemProperty -Path $runKey -Name nimget -ErrorAction SilentlyContinue)) 'Uninstall left the installed app registered for launch at login'
    Assert-Condition ((Test-Path -LiteralPath "$dataDir\nimget.db") -and (Test-Path -LiteralPath "$dataDir\token")) 'Uninstall removed saved application data'
    Assert-Condition ((Get-Content -LiteralPath $destination -Raw) -ceq $payload) 'Uninstall changed or removed a downloaded file'
    Write-Host 'Fresh install, launch, queued download after upgrade, and non-destructive uninstall passed.'
} finally {
    Stop-TestApp $app
    if ((Test-Path -LiteralPath $uninstaller) -and (Test-Path $uninstallKey)) { $null = Run-Setup $uninstaller '/S' }
    $fixture | Remove-Job -Force
}
