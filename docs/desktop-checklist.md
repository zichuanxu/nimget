# Desktop manual checklist

Run on macOS and Windows after changes to `app/`, using the startup steps in
[source.md](source.md). Start from a clean data directory (`~/Library/Application Support/nimget` or
`%AppData%\nimget`) and with no `nimget service` running.

## Startup and shell

- [ ] The app opens a window; `nimget list` from a terminal works while it runs (the loopback API is up).
- [ ] With `nimget service` already running, the app shows the "could not start" banner instead of crashing.
- [ ] Launching the app a second time focuses the first window instead of starting another.
- [ ] Closing the window hides it; downloads continue. The tray (menu bar) icon shows it again.
- [ ] Tray menu: Pause all, Resume all, and Quit work. Quit lets running downloads checkpoint; the next start resumes them.
- [ ] On macOS, clicking the Dock icon with the window hidden shows it.
- [ ] On macOS the sidebar is translucent and the window can be dragged by the header and the sidebar top; buttons and the search box there still respond to clicks.
- [ ] Settings → General → Language: English and 简体中文 switch every label, the tray menu, and notifications; "System" follows the OS language.
- [ ] Settings → General → Appearance: System, Light, and Dark apply at once and survive a restart.
- [ ] NimGet's icon is legible beside the sidebar title in both Light and Dark appearances.

## Adding downloads

- [ ] Add a URL: the file name comes from the server and lands in the category folder (for example `Downloads/Documents`).
- [ ] Add two URLs at once (one per line): both are queued.
- [ ] Choose a folder: the download goes there; the folder is listed under Settings → Advanced.
- [ ] Give a file name: it is used as is.
- [ ] Drag a link from a browser onto the window: the add dialog opens with the URL.
- [ ] Paste a browser "Copy as cURL" command (bash and cmd variants): the URL and the cookie/referer headers are imported.
- [ ] Copy a URL, then a cURL command, in another app: the clipboard offer appears; Download opens the add dialog; Dismiss hides it. Turning the monitor off in Settings stops the offers.

## Queue

- [ ] Progress bar, speed, time left, and the sidebar speed card update while downloading.
- [ ] Sidebar filters (Active, Paused, Completed, Failed) and their counts match the list; search filters by name and URL.
- [ ] Hovering a row shows its quick actions; right-click and the ⋯ button open the same actions menu.
- [ ] Keyboard: ⌘/Ctrl+N new download, ⌘/Ctrl+, settings, ⌘/Ctrl+F search, arrows select, Space pauses or resumes, Enter opens, Delete asks to delete, Esc closes dialogs.
- [ ] Pause, Resume, Retry, and Delete (with and without "also delete the file") behave as labelled.
- [ ] The top Resume all button is disabled without paused downloads; Pause all is disabled without queued or running downloads. In a mixed queue, both are available.
- [ ] Open launches a completed file; Show in folder reveals it in Finder or Explorer.
- [ ] A download that fails with 401 or 403 offers Re-authenticate; pasting a fresh cURL command retries it.
- [ ] A high-priority download starts before normal ones when the queue is full.

## Settings

- [ ] Downloads at once, connections, per-server connections, and total speed limit take effect after Save.
- [ ] A queue window that excludes the current time stops running downloads; removing it restarts them.
- [ ] Time-of-day speed rules change the total speed.
- [ ] Proxy: System, No proxy, and Manual (with a local proxy) work; invalid values show an error.
- [ ] Site settings JSON with a password: the password shows as `********` after reopening Settings and still works.
- [ ] Notifications arrive for completed and failed downloads (macOS: from the `.app` bundle only), and stop when turned off.
- [ ] "Always ask where to save" opens the folder picker on Add.
- [ ] "Start NimGet when I log in" registers the app (check after logging out and in).

## Extras (M4)

- [ ] Add `https://…/img[001-005].jpg`: the dialog shows "Batch pattern: 5 URLs" and queues five downloads.
- [ ] Export writes a JSON file without cookies; Import of that file, and of a text file of URLs, queues them again.
- [ ] Settings → Downloads → "When the queue finishes: Sleep" with a short download: a 30 s countdown appears when the queue empties; Cancel stops it; letting it run puts the computer to sleep once, and the choice resets to Nothing.
- [ ] Settings → Downloads → "When the queue finishes: Shut down" shows the same countdown (cancel it, or test on a VM).
- [ ] "Open files when they finish" opens each completed file.

## HLS (M5)

- [ ] Add a VOD `.m3u8` URL: it downloads as one `.ts` file in `Video` that plays in VLC or QuickTime (via MP4 conversion).
- [ ] An AES-128 encrypted stream plays after download.
- [ ] Pause and resume an HLS download: it continues from the finished segments.
- [ ] With ffmpeg installed, "Convert to MP4" creates a playable `.mp4` next to the `.ts`; without ffmpeg the button is hidden.

## Magnet downloads

- [ ] Paste a public magnet in **New Download**, choose **Save to**, and verify that its final output appears in the chosen directory after completion.
- [ ] With an effective proxy, verify that the task waits for direct connection permission. Save the permission in **Download details**, revoke it during transfer, and verify that the task keeps its progress. Grant it again, restart NimGet, and check that the same task continues.
- [ ] Use **Open** and **Show in folder** for a completed single-file magnet and for a completed multi-file magnet. Check the selected file or open folder in Finder or Explorer.
- [ ] Edit one file and add a new file to a completed multi-file magnet directory, then use **Also delete the file**. Verify that NimGet preserves both changes and removes the unedited download files.

### Repeatable magnet acceptance checks

These checks use local fixture peers, a tracker, DHT nodes, and an HTTP server. They require no public swarm. Start from the repository root with the Go and Node versions listed in the [source guide](source.md).

```sh
go test -race ./cmd/nimget ./internal/service -run 'Magnet|MixedQueue|UploadLimit' -count=1
(cd app/frontend && npm ci && npm run build && npx playwright install chromium && npm test -- magnet.spec.ts download-files.spec.ts)
(cd app && go test -tags production ./...)
```

| Acceptance | Evidence |
| --- | --- |
| Desktop entry | `magnet.spec.ts` verifies the New Download interaction through generated bindings at the Wails transport. `TestDesktopAddCompletesMagnetDownload` calls the desktop binding against the running service and checks the downloaded bytes for v1 and hybrid magnets. |
| CLI and queue upgrade | `TestCLICompletesMagnetAlongsideUpgradedHTTPTask` submits a v2 magnet through `nimget add` to the authenticated service backed by a pre-magnet SQLite queue. It checks proxy blocking, CLI consent, and completion of both outputs byte for byte. |
| Protocol and networking | The service tests transfer tracked v1, v2, and hybrid fixtures, including nested directories and empty files. They also cover trackerless DHT, shared download and upload limits, per-task proxy consent across restart, and disconnect on revocation. |
| Recovery and filesystem | The service tests resume verified pieces, interrupt publication immediately before and after the exclusive move, preserve occupied destinations, and protect edited, replaced, and added files during deletion. `TestDesktopOpensAndRevealsFileAndDirectoryOutputs` checks which filesystem path the desktop binding sends to the native shell. |

The [CI workflow](../.github/workflows/ci.yml) runs native filesystem and service tests on macOS and Windows, builds both desktop targets and runs their desktop binding tests. It also cross-builds the CLI for macOS amd64 and arm64, Windows amd64, and Linux amd64 and arm64. Use the manual steps above to check New Download in the native WebView and verify Finder and Explorer behavior in the installed desktop app.

## Source startup

- [ ] From a fresh checkout, `npm ci` and `npm run build` succeed in `app/frontend`, then `go run .` starts the desktop app from `app/`.
- [ ] After updating the source and rebuilding the frontend, restarting the app preserves settings and resumes the queue.

## Secrets

- [ ] After adding a download with a cookie, the cookie value does not appear in `nimget.db` (for example `strings nimget.db | grep <value>` finds nothing).
