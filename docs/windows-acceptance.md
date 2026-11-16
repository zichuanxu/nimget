# Windows integration acceptance — November 16, 2026

This record covers the unpublished `v1.1.0-rc.1` candidate at source commit `23872235e21e93831175ca029bd1a9dafddce1dd`. The tested host was Windows 11 Home x64 25H2 (26200.9974), with WebView2 157.0.4341.46, Chrome 157.0.8103.51, and Edge 157.0.4341.60. Windows 10 runtime testing was waived. This candidate has remaining acceptance gaps and is not marked release-ready.

| Release asset | SHA-256 |
| --- | --- |
| `NimGet-v1.1.0-rc.1-windows-x64-setup.exe` | `b079f1fbad0dd8bac45ee84549cc82419b3c2d397ea3c2150ce68af420c1281f` |
| `NimGet-v1.1.0-rc.1-chromium-extension.zip` | `609ddf9eb5cb3d482b8edb6d6aef7a0f0d9a43ad2d543b360f224cb2ad7277ba` |
| `NimGet-v1.1.0-rc.1-release.json` | `9d8c7430d1b71c49ac08379090c25c0145b084c9bac88c1eadcb3f559be346ff` |

Go 1.27.4, Node 26.13.1, npm 11.20.0, NSIS 3.13, and PowerShell 7.7.0 built the bundle. The script compared two builds byte for byte, verified the installer's embedded source commit, and rejected changed metadata, changed extension content with a recomputed checksum, and extra files.

## Tests against the installer and archived extension

| Check | Observed result |
| --- | --- |
| Install, upgrade, uninstall | The `v1.0.2-rc.3` → `v1.1.0-rc.1` lifecycle script confirmed the Start menu shortcut, correct Installed Apps versions, launch with Windows-only PATH, running-app refusal, preservation of settings and a queued task, completion with expected bytes, and removal of program/registration while retaining the database and downloaded file. A fresh direct installation of the candidate and its later uninstall also passed. |
| Native WebView2 and folder picker | In the installed app, Ctrl+N opened the named New download dialog. Tab moved from the Links input to Choose and Enter opened the Windows folder picker. Selecting a disposable directory restored the focus ring to Choose. The app wrote the fixture bytes to that directory. The redirected test profile initially lacked its Desktop folder and the picker displayed a location notice; creating that folder and dismissing the notice let the test continue. |
| Real queue events, paging, and bulk pause | The installed service accepted 105 additional queued tasks while the app was open, for 108 total. The app showed rows 1–50 and 51–100 on separate pages. Selection survived changing pages. Bulk pause reported 2 succeeded, 0 failed and 0 skipped; the service reported exactly the two selected fixture tasks paused. |
| Archived extension in Chrome and Edge | Both branded browsers loaded the extracted, unmodified archive (`1.1.0` / `v1.1.0-rc.1`) in disposable profiles. Automatic capture started off. An actual extension request appeared in desktop settings; a fixture helper applied the normal store approval and revocation operations to the disposable database. Each native right-click menu sent a loopback public link into the installed process, which persisted exactly one completed task and wrote the expected 262,144 bytes. Each popup reflected pairing and revocation. This run covered the native link action; earlier browser tests exercised the full desktop approval UI. |
| Final uninstall retained work | The second uninstall removed the disposable installation and registration. The profile database and all three native acceptance files remained. |

The final archive verifier compared extension source files against the checkout; the archive changes the manifest version fields for release. Earlier checkpoints used the same browser code and ran both branded browsers against the local Go service and controlled HTTP fixtures. They covered manual handoff retry and desktop interruption, protected links and denial, automatic capture opt-in, host and file exclusions, POST/redirect/private fallback, lost and malformed receipts, timeout, explicit cancellation failure, and same-task acknowledgement reconciliation. See [browser handoff acceptance](browser-handoff.md#automatic-capture-acceptance) and [pairing](browser-pairing.md#acceptance-steps). A separate test used each browser's ordinary **Load unpacked** flow and a normal close and relaunch of the same profile. Both retained the exact optional cookie, download and request-observation grants; this check used the workspace extension, without the installed desktop. Those cases were not all repeated from the final archive. A test launch flag did not grant optional permissions in the archived-extension rerun.

The Go engine and service regressions cover checkpoint recovery, byte comparison after a signed URL change, explicit restart on mismatch, and supported/unsupported HLS. The production frontend preview covers the corresponding UI, partial batch retry, task edits, structured settings, keyboard navigation, and bulk outcomes. Preview tests stub the Wails transport. A longer native automation run could not attach to WebView2: Wails suppressed debug environment settings in the artifact, and a temporary build with explicit runtime debugging arguments and the devtools tag still did not expose a listening endpoint on this host. The installed candidate checks above used its UI directly. The full scripted native WebView2 pass and native screen reader behavior remain unverified.

## Reproduce the build and checks

From the tested commit, set `$nsis` to the path of NSIS 3.13's `makensis.exe`. Set `$previousInstaller` to the verified older `v1.0.2-rc.3` setup executable. Run the installer lifecycle script from an interactive account with no existing NimGet installation.

~~~powershell
./scripts/test-windows-release.ps1 -Version v1.1.0-rc.1 -OutputDirectory dist/acceptance-18 -MakeNSIS $nsis
./scripts/prepare-windows-release.ps1 -Version v1.1.0-rc.1 -OutputDirectory dist/acceptance-18 -VerifyOnly
./scripts/test-windows-installer.ps1 -PreviousInstaller $previousInstaller -PreviousVersion v1.0.2-rc.3 -Installer ./dist/acceptance-18/v1.1.0-rc.1/NimGet-v1.1.0-rc.1-windows-x64-setup.exe -Version v1.1.0-rc.1
~~~

Follow the [browser acceptance steps](browser-pairing.md#acceptance-steps) with a disposable profile and the extracted archive. For native queue acceptance, use a local HTTP fixture and an isolated home and application data directory. Use an off-hours schedule to seed queued tasks through the authenticated API, then check page counts, select tasks across pages, and pause them through the desktop. Compare the UI's two outcomes to the persisted task states.

The following gates passed on this source:

- Windows CLI and core: `go test ./...`, `go test -race ./...`, `go vet ./...`, and `go build ./cmd/nimget`. CLI cross-builds with CGO disabled passed for Windows amd64, Darwin arm64/amd64, and Linux arm64/amd64.
- Windows desktop: `go test -tags production ./...`, the same test with `-race`, `go vet -tags production ./...`, and the CGO-disabled production GUI build from `app`. Wails 3.0.0-beta.26 binding regeneration produced no content changes. The bundle built the frontend with `npm ci` and `npm run build`.
- Extension tests: `node --test tests/extension/*.test.mjs`, 10 passed. Frontend dependency audit via `npm audit --registry=https://registry.npmjs.org` reported zero vulnerabilities. Workflow lint with actionlint 1.7.12 passed.
- WSL Ubuntu: `go vet ./...` and `go test -race ./...` passed. macOS runtime and the hosted CI matrix were not run locally.
- Edge production preview: after generating the 2,000-task fixture with `go test ./internal/service -run '^TestLargePersistedQueueTracksProgress$' -count=1 -v` and `NIMGET_QUEUE_FIXTURE`, `NIMGET_TEST_BROWSER=msedge`, `NIMGET_TEST_PREVIEW=1`, `NIMGET_TEST_PORT=19262`, and `npm test -- --workers=1` ran all 31 tests, including the opt-in benchmark. It mounted 50 queue rows, delivered 128 replayed progress events in 3.252 s, and recorded no long tasks. An earlier full Edge run timed out at `keyboard.spec.ts:21`: the first Tab did not focus New download. The same assertion passed eight focused reruns and the full run with the benchmark enabled. The original intermittent failure remains unexplained.

The Linux performance gate ran in WSL with Go 1.27.4 and aria2c 1.37.0:

~~~sh
go build -o nimget ./cmd/nimget
go build -o bench ./tools/bench
./bench -nimget ./nimget -aria2c /path/to/aria2c -gate
~~~

| Measurement | NimGet | aria2c |
| --- | ---: | ---: |
| Capped throughput | 63.3 MiB/s | 63.5 MiB/s |
| Uncapped throughput | 340.0 MiB/s | 576.9 MiB/s |
| Uncapped CPU cores normalized to 1 Gbps | 1.12 | 0.19 |
| Uncapped peak RSS | 18.2 MB | 29.6 MB |

Transferred bytes matched. The gate failed the one-core CPU threshold, using measured process CPU seconds divided by the time to transfer the same number of bytes at 125 MB/s. This is a local WSL result; it does not establish a hosted Linux regression, Windows performance, or IDM parity.

## Remaining release checks

A clean development-tool-free Windows image, a host without WebView2, internet-download SmartScreen and policy prompts, full scripted native UI coverage, and screen reader testing were unavailable. No tag, release, or asset was published.

## Final review regressions

- Automatic capture records whether Chromium sent cookies and has the service compare the observed attachment with a bounded probe before it saves a task. Chrome 157.0.8103.51 and Edge 157.0.4341.60 preserved browser downloads for unmarked cookie authentication and a status-200 login fallback. Both retained public capture with incidental cookies, explicitly authorized same-origin cookies, exclusions, and acknowledgement and cancellation failure handling. These runs used the unpacked workspace extension, the real Go service, and loopback fixtures. See `TestAutomaticHandoffRequiresMatchingReplayBeforeReceipt` for the API regression.
- Ordinary partial files use exclusive creation and bind the checkpoint to the opened file. Foreign sidecars stay in place and the error explains how to proceed. HLS preserves an unrelated assembly path or work directory and refetches when the plan's request URLs or context change. The Go engine and HLS suites cover collisions, changed queries, and same-source resume.
- Pasted cURL request headers stay at their original HTTP origin. Frontend tests cover scheme changes, a different port, and an equivalent default port.
- Ordinary summaries omit source and destination strings. Exact links can be backed up explicitly and reimported. The service and CLI regressions cover reflected HTTP response body secrets, path and filename credentials, rejected summary import, numbered partial import failures, and legacy import support.
- Site settings reject new case-insensitive header collisions while preserving unchanged saved rules. Queue reconciliation and retry-list focus handling have dedicated code paths, with the queue's existing sequence and keyboard tests retained. A delayed-bootstrap keyboard test waits for the intended button to mount before pressing Tab and still checks focus containment and restoration.
