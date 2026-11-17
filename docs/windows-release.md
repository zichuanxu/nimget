# Windows release assets

For releases with a Windows bundle, download all four assets from the same version on the [NimGet Releases page](https://github.com/amemiya02/nimget/releases). The examples use `v1.2.3`.

| Asset | Purpose |
| --- | --- |
| `NimGet-v1.2.3-windows-x64-setup.exe` | Unsigned, per-user Windows desktop installer |
| `NimGet-v1.2.3-chromium-extension.zip` | Unpacked extension for Chrome and Edge |
| `NimGet-v1.2.3-release.json` | Release version, source commit, and build tool versions |
| `SHA256SUMS.txt` | SHA-256 hashes for the installer, extension archive, and metadata |

Open PowerShell in the download directory. Compare each hash to the line for the same filename in `SHA256SUMS.txt` from the trusted NimGet Release page before running the installer or extracting the extension.

```powershell
Get-FileHash -Algorithm SHA256 -LiteralPath .\NimGet-v1.2.3-windows-x64-setup.exe,
    .\NimGet-v1.2.3-chromium-extension.zip, .\NimGet-v1.2.3-release.json
```

Follow the [Windows installation guide](windows-installer.md) for WebView2, SmartScreen, upgrade, and uninstall steps. The [browser pairing guide](browser-pairing.md) covers installing and updating the unpacked extension. The [browser handoff guide](browser-handoff.md) explains manual handoff, website session permission, automatic capture, and browser fallback.

The desktop can show a link to new release notes if you enable its GitHub update check. Upgrading the app and the unpacked extension requires downloading and verifying the newer files and following the two guides. From v1.1.0, releases also include a macOS disk image, prepared by the **Prepare macOS release** workflow; see the [README](../README.md#install-on-macos). Linux CLI and service builds are available [from source](source.md).

## Prepare the release bundle

On Windows, check out the intended source commit with a clean working tree. Use PowerShell 7.4 or later and the Go, Node.js, and NSIS versions specified by the [release preparation workflow](../.github/workflows/windows-release.yml). To reproduce a particular bundle, also use its recorded PowerShell and npm versions and the same Git checkout line-ending settings.

```powershell
.\scripts\prepare-windows-release.ps1 -Version v1.2.3
.\scripts\prepare-windows-release.ps1 -Version v1.2.3 -VerifyOnly
```

The script writes the four verified assets to `dist/release/v1.2.3`. Use `-MakeNSIS` if `makensis.exe` is outside `PATH` and `-OutputDirectory` to choose another parent directory. Each preparation requires a new output directory for that version. `-VerifyOnly` checks the checksums, the source commit in the installer and release metadata, and the extension archive contents against the current checkout. The extension's display version matches the release tag, including any prerelease suffix. Its numeric version uses the three numeric fields of the tag.

To validate repeatability and rejection of changed or extra assets locally, run the release test on the same checkout. It writes to `dist/release-test` by default.

```powershell
.\scripts\test-windows-release.ps1 -Version v1.2.3
```

## Prepare with GitHub Actions

Run **Prepare Windows release** from Actions once the workflow is on the default branch. Select the intended ref and enter the version. For a tagged release, the input must match the tag. The workflow runs the Go race tests, vet and build, extension tests, frontend audit and build, production desktop tests, Edge frontend regressions, and a byte-for-byte rebuild check. It retains the four verified assets together for seven days as a workflow artifact on a standard GitHub-hosted runner. The public repository workflow uses no paid runner, signing certificate, or browser-store account.

Before publication, review the standard cross-platform CI and performance checks for the commit. Download the workflow artifact, verify it at that commit, and exercise the downloaded installer and extension on Windows 11 with Chrome and Edge. Use an interactive account for the [installer lifecycle check](windows-installer.md#verify-an-installer) and follow the [browser acceptance steps](browser-pairing.md#acceptance-steps). When the results are approved for publication, attach the four files to the GitHub Release for the matching tag. Release preparation outputs an Actions artifact. Publication is a separate maintainer operation.

The [Windows integration acceptance record](windows-acceptance.md) captures the local release candidate, tested flows, and remaining checks.
