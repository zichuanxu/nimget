# NimGet

<img src="app/build/appicon.png" alt="NimGet logo" width="96">

NimGet is a lightweight, open-source download manager built in Go for macOS and Windows. Its `nimget` CLI and background service also run on Linux.

- Up to 32 connections per download, rebalanced as they finish; byte-level resume, even after a crash.
- Queue with priorities, per-host connection caps, speed limits, and a schedule.
- HLS (`.m3u8`, AES-128) to one `.ts` file, optional MP4 conversion with ffmpeg.
- Desktop app: tray icon, notifications, drag and drop, cURL import, clipboard monitor, batch URLs, sleep or shut down when done; English and Simplified Chinese, light and dark themes.

## Run from source

NimGet is distributed as source code. Requires Git and Go 1.25+. The desktop app also needs Node.js 22.12+ and, on macOS, the Xcode command line tools; on Windows it needs the WebView2 Runtime.

```bash
git clone https://github.com/zichuanxu/nimget.git
cd nimget
```

Run the CLI directly:

```bash
go run ./cmd/nimget download https://example.com/file.iso ./file.iso
```

Run the desktop app (macOS and Windows; `app/` is its own Go module):

```bash
cd app/frontend
npm ci
npm run build
cd ..
go run .
```

For a headless queue, run `go run ./cmd/nimget service` from the repository root and keep that terminal open. In another terminal, use `go run ./cmd/nimget add URL` and `go run ./cmd/nimget list`. The desktop app runs the same service, so these commands also work while it is open. Quit any other NimGet app or service before starting one: they share port 51000 and a single-instance lock.

[docs/source.md](docs/source.md) covers local builds, UI live reload, and updating your checkout. [docs/usage.md](docs/usage.md) covers every command, the settings, and the HTTP API.

## Development

Run `go test ./...` at the repository root and again in `app/`. To build a local CLI for the examples in the usage reference, run `go build ./cmd/nimget` from the repository root; this creates `nimget` (`nimget.exe` on Windows).

After changing a bound Go method or type, regenerate the TypeScript bindings (CI checks them):

```bash
go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-beta.26
cd app && wails3 generate bindings -clean=true -ts -f '-tags production'
```

The design and roadmap are in [DESIGN.md](DESIGN.md); [docs/desktop-checklist.md](docs/desktop-checklist.md) is the manual test list.

## Repository layout

```text
cmd/nimget/             Cobra CLI: service, add, list, set, pause, resume, retry, delete, settings, export, import, download, version
internal/download/    Shared queue model, errors, and event contract
internal/engine/      Download engine: work-stealing ranges, checkpoints, validation
internal/filelock/    OS advisory file locks
internal/logging/     slog setup and URL redaction
internal/store/       SQLite persistence
internal/manager/     Queue, priorities, host caps, limits, schedule, naming, confinement
internal/settings/    Settings document and schedule rules
internal/netproxy/    Manual and system proxy selection
internal/hls/         HLS playlists, AES-128, TS/fMP4 concatenation, segment resume
internal/remux/       Optional ffmpeg remux of .ts to .mp4
internal/batch/       Batch URL patterns
internal/queuefile/   Queue import and export
internal/power/       Sleep and shut down
internal/update/      Optional release notices for versioned local builds
internal/quarantine/  Marks completed files as downloaded from the internet
internal/secrets/     AES-GCM sealing with a key in the OS keyring
internal/curlimport/  "Copy as cURL" parser
internal/api/         Authenticated loopback JSON and SSE API
internal/client/      Service client
internal/service/     Service composition, lifecycle, paths, and token
tools/bench/          Performance gate against aria2c
docs/                 Source startup guide, usage reference, desktop checklist
app/                  Desktop app (Wails v3 module): bindings, tray, React frontend
DESIGN.md             Design contract and M0–M6 roadmap
```

## License

Apache-2.0. See [LICENSE](LICENSE) and [NOTICE](NOTICE).
