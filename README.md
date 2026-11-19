<p align="center">
  <img src="app/build/appicon.png" alt="NimGet logo" width="88">
</p>

<h1 align="center">NimGet</h1>

<p align="center">An open-source download manager for Windows and macOS, with a CLI and headless queue that also run on Linux.</p>

<p align="center">
  <a href="https://github.com/amemiya02/nimget/releases/latest"><strong>Download NimGet</strong></a>
  · <a href="#benchmarks">Benchmarks</a>
  · <a href="docs/usage.md">Documentation</a>
</p>

![NimGet desktop queue on Windows with example downloads](docs/assets/nimget-desktop.png)

<p align="center"><sub>Desktop preview with example downloads</sub></p>

- **Parallel HTTP downloads.** Use up to 32 connections per file, rebalance work as connections finish, and resume eligible downloads at byte offsets after a crash.
- **A queue you control.** Set priorities, per-host connection caps, speed limits, and a download schedule.
- **Browser handoff.** Send links from Chrome or Edge, or opt in to automatic capture. Use an existing website session when a download needs it. [Browser integration](docs/browser-handoff.md)
- **Magnet links and HLS.** Download public BitTorrent v1, v2, and hybrid magnets, or save supported HLS streams and optionally convert them to MP4 with FFmpeg. [Supported downloads](docs/usage.md)

## Benchmarks

NimGet finished first in all five rounds of two Windows 11 loopback tests. For an uncapped 512 MiB download, it delivered **3.2× aria2’s throughput** and **4.9× Windows curl’s throughput**.

| Test | NimGet | Windows curl | aria2 |
| --- | ---: | ---: | ---: |
| 512 MiB, uncapped. NimGet used 8 connections and aria2 used 16. | **1.24 s** | 6.14 s | 3.97 s |
| 64 MiB, 4 MiB/s per connection and 350 ms response delay. NimGet and aria2 each used 6 connections. | **3.52 s** | 16.39 s | 4.11 s |

Numbers are median completion times. The benchmark verifies every output with SHA-256 and removes it after each run. Public download speeds vary by source and network. [Full results and reproduction commands](docs/http-benchmarks.md)

## Install on Windows

Get the x64 setup file and `SHA256SUMS.txt` from the [latest release](https://github.com/amemiya02/nimget/releases/latest). NimGet runs on Windows 10 and 11 with the WebView2 Runtime. [Installation and checksum guide](docs/windows-installer.md)

## Install on macOS

Get the universal DMG and `SHA256SUMS-macos.txt` from the [latest release](https://github.com/amemiya02/nimget/releases/latest). NimGet runs on Apple silicon and Intel Macs with macOS 13 or later.

Verify the image with `shasum -a 256 NimGet-*.dmg`, then open it and drag NimGet to Applications. If macOS blocks the ad-hoc-signed build on first launch, go to **System Settings → Privacy & Security** and select **Open Anyway**.

## Get started

1. Open NimGet, choose **New download**, and paste an HTTP(S) URL or magnet link.
2. For Chrome or Edge, download the Chromium extension ZIP from the same release and [pair it with NimGet](docs/browser-pairing.md).
3. To update the desktop app, select **Check for updates** in the sidebar. NimGet verifies the installer checksum before adding it to your completed downloads.

## Build from source

Use Git and Go 1.27 or later for the CLI on Windows, macOS, or Linux.

```sh
git clone https://github.com/amemiya02/nimget.git
cd nimget
go run ./cmd/nimget download https://github.com/amemiya02/nimget/releases/latest/download/SHA256SUMS.txt ./nimget-checksums.txt
```

Start the headless queue with `go run ./cmd/nimget service`, then use `go run ./cmd/nimget add URL` and `go run ./cmd/nimget list` in another terminal. The same client commands work with the desktop app while it is running.

The desktop build also needs Node.js 22.12 or later and the platform prerequisites in the [source guide](docs/source.md).

```sh
cd app/frontend
npm ci
npm run build
cd ..
go run .
```

## Contributing

Run `go test ./...` from the repository root and again from `app/`. See the [source guide](docs/source.md) for the development workflow, the [desktop checklist](docs/desktop-checklist.md) for manual checks, and [DESIGN.md](DESIGN.md) for the engine and architecture.

## License

[Apache-2.0](LICENSE). See [NOTICE](NOTICE) for third-party notices.
