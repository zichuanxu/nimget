# Install NimGet on Windows

Use the x64 installer on Windows 10 or 11 with the Microsoft Edge WebView2 Evergreen Runtime. Go and Node.js are build tools. The installed desktop app includes its frontend and local download service. FFmpeg is optional for converting supported HLS downloads to MP4.

1. Download the installer and `SHA256SUMS.txt` from the same [NimGet release](windows-release.md). Run Get-FileHash in PowerShell and compare the result to the installer line in `SHA256SUMS.txt` before opening Setup.

   ~~~powershell
   Get-FileHash -Algorithm SHA256 .\NimGet-v1.2.3-windows-x64-setup.exe
   ~~~

2. Run Setup as the Windows user who will use NimGet. Setup defaults to %LOCALAPPDATA%\Programs\nimget and adds NimGet to the Start menu and to Installed apps (Windows 11) or Apps & features (Windows 10). Administrator rights are unnecessary.
3. If Setup reports that WebView2 is missing, install the Evergreen Runtime from Microsoft and rerun Setup. Use the Evergreen Bootstrapper online or the x64 Evergreen Standalone Installer on an offline PC. The Edge browser is separate from the runtime. [Microsoft WebView2 downloads](https://developer.microsoft.com/microsoft-edge/webview2/#download)
4. Launch NimGet from the Start menu. Closing the window leaves it running in the tray. Use **Quit NimGet** in the tray menu to stop it.

The installer and app are unsigned. Windows may show a Microsoft Defender SmartScreen warning for a download with limited reputation or show an unknown publisher. Smart App Control or managed security policy may block the package. A checksum verifies a download against the published checksum for that file. [Microsoft SmartScreen guidance](https://learn.microsoft.com/windows/apps/package-and-deploy/smartscreen-reputation)

## Upgrade

Download and verify the newer installer from the [release page](windows-release.md). Choose **Quit NimGet** from the tray menu, then run Setup under the same Windows account. Setup upgrades the registered install location. Launch NimGet to reopen the saved queue and continue eligible tasks. Paused tasks and schedule restrictions still apply. Settings and the queue database live in %APPDATA%\nimget. The packaged app's WebView2 profile lives in %APPDATA%\nimget.exe and holds local interface preferences. If you use Chrome or Edge integration, [update the unpacked extension](browser-pairing.md#update-the-extension) from the same release.

## Uninstall and retained data

Quit NimGet from the tray and uninstall it from Settings. The uninstaller removes the installed program, its Start menu shortcut, its Installed apps entry, and any NimGet launch-at-login entry pointing to that installation.

Uninstall keeps application data, including the queue, settings, logs, WebView2 profile and the NimGet key in Windows Credential Manager. A reinstall by the same user can reopen the saved queue. Downloaded files and partial files stay in their chosen download directories. The shared WebView2 Runtime stays installed.

## Build the installer

On Windows, install Go 1.25+, Node.js 22.12+ and PowerShell 7.4+. Install [NSIS 3.13](https://sourceforge.net/projects/nsis/files/NSIS%203/3.13/). Put makensis.exe on PATH or supply its location with -MakeNSIS.

From the repository root:

~~~powershell
.\scripts\build-windows-installer.ps1 -Version v1.2.3
~~~

The script builds the frontend with npm ci, generates Windows resources with the Wails version pinned by the desktop Go module and builds the production desktop executable with CGO disabled. It writes dist/windows/NimGet-v1.2.3-windows-x64-setup.exe. Use a vMAJOR.MINOR.PATCH tag, optionally with a prerelease suffix. -OutputDirectory selects a different output path.

For unattended installation, run Setup with /S. Exit code 2 means NimGet must be closed, 3 means the Windows version or CPU is unsupported, 4 means WebView2 is missing and 5 means another installer or uninstaller is running. An uninstaller run outside the registered install directory exits with code 6.

## Verify an installer

Run the lifecycle check from an interactive, disposable Windows account with WebView2 installed and no previous NimGet installation or desktop process running.

~~~powershell
.\scripts\test-windows-installer.ps1 `
  -PreviousInstaller .\dist\windows\NimGet-v1.2.3-windows-x64-setup.exe -PreviousVersion v1.2.3 `
  -Installer .\dist\windows\NimGet-v1.2.4-windows-x64-setup.exe -Version v1.2.4
~~~

The check uses an isolated install directory and app profile, launches the installed app with a Windows-only PATH and uses a local download fixture. It checks the executable and installer versions, preservation of a queued download and settings through an in-place upgrade, and retained application data and downloaded bytes after uninstall. It prints the temporary acceptance directory for inspection.

The runtime acceptance target for this change is Windows 11. Run this on a clean Windows 11 machine without Go or Node.js when available. With WebView2 absent, check that Setup explains the prerequisite and exits without installing NimGet, then install the Evergreen Runtime and repeat. On downloaded artifacts, record SmartScreen or policy prompts if they appear.

Local acceptance passed on Windows 11 Home x64 25H2 (26200.9974) with WebView2 157.0.4341.46. The test installed a build from the v1.0.1 tagged app source using the restored installer, upgraded to v1.0.2-rc.1, completed the saved queued download and verified data retention after uninstall. Each launch used an isolated profile and a Windows-only PATH on the development host. A clean OS image, absent-WebView2 installation and prompts for an internet-downloaded package remain unverified. Windows 10 runtime testing was skipped as requested.
