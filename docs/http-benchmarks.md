# HTTP download benchmarks on Windows

## Three-client benchmark

On Windows 11 build 26200 with an Intel Core i9-13900H and an NTFS SSD, NimGet completed the uncapped 512 MiB loopback download faster than Windows curl 8.22.0 and aria2 1.37.0 in all five rounds. All clients wrote to the same volume on the same machine. The tools ran one at a time in rotating order. Timing covers process start through completion, followed by SHA-256 verification and immediate removal of the output and any sidecars.

| 512 MiB uncapped loopback | Connections | Median time | Median MiB/s | SHA-256 |
| --- | ---: | ---: | ---: | --- |
| NimGet | 8 | 1.24 s | 412.51 | 5/5 |
| Windows curl | 1 | 6.14 s | 83.35 | 5/5 |
| aria2 | 16 | 3.97 s | 129.12 | 5/5 |

NimGet also finished first in all five rounds with a 64 MiB fixture capped at 4 MiB/s per connection and a 350 ms delay before each response. NimGet and aria2 used the same six-connection setting.

| 64 MiB capped and delayed loopback | Connections | Median time | Median MiB/s | SHA-256 |
| --- | ---: | ---: | ---: | --- |
| NimGet | 6 | 3.52 s | 18.16 | 5/5 |
| Windows curl | 1 | 16.39 s | 3.91 | 5/5 |
| aria2 | 6 | 4.11 s | 15.59 | 5/5 |

In a separate loopback test, the server ignored Range and waited 250 ms before responding with a 16 MiB file. Reusing the complete probe response reduced NimGet's median time from 0.73 s at commit `9e7a018` to 0.43 s, about 41% less time over seven rounds. The engine reads the response it already has, saving a GET. aria2 also hands the received response to its download command after processing the headers. [aria2 HTTP response handling](https://github.com/aria2/aria2/blob/release-1.37.0/src/HttpResponseCommand.cc)

| 16 MiB, no Range, 250 ms response delay | Median time | Median MiB/s | SHA-256 |
| --- | ---: | ---: | --- |
| NimGet baseline | 0.73 s | 22.02 | 7/7 |
| NimGet | 0.43 s | 37.36 | 7/7 |
| Windows curl | 0.38 s | 42.56 | 7/7 |
| aria2 | 0.45 s | 35.29 | 7/7 |

Build and run the comparison from the repository root in PowerShell. Set `$aria` to the installed [aria2c executable](https://github.com/aria2/aria2/releases/tag/release-1.37.0).

```powershell
$nimget = Join-Path $env:TEMP 'nimget-current.exe'
$bench = Join-Path $env:TEMP 'nimget-bench.exe'
$curl = Join-Path $env:SystemRoot 'System32\curl.exe'
go build -trimpath -o $nimget ./cmd/nimget
go build -o $bench ./tools/bench
& $bench -nimget $nimget -aria2c $aria -curl $curl -size 512 -rate 0 -connections 8 -aria-connections 16 -rounds 5
& $bench -nimget $nimget -aria2c $aria -curl $curl -size 64 -rate 4 -latency 350ms -connections 6 -rounds 5
```

For the single-connection scenario, use `-size 16 -rate 0 -latency 250ms -no-ranges -connections 1 -rounds 7`. Pass an executable built from commit `9e7a018` with `-baseline` to reproduce the before-and-after comparison. aria2 uses `-k1M`, `--file-allocation=none`, and its default disk cache. These figures measure local loopback performance. The public HTTPS measurements below show how results vary by source.

## Earlier Windows measurements

Measured on November 19, 2026, on Windows 11 build 26200, an Intel Core i9-13900H, Wi-Fi, and an NTFS SSD. The v1.4.4 baseline used the same source tree as tag `v1.4.4`, built with Go 1.27.4. The updated build includes the initial range balancing change. Tests used Windows curl 8.22.0, Microsoft Edge 157.0.4341.60, and aria2 1.37.0. [Windows curl](https://learn.microsoft.com/en-us/windows/curl/) · [aria2 release](https://github.com/aria2/aria2/releases/tag/release-1.37.0)

Complete-file tests timed the download and verified the output size and SHA-256 after timing. Clients wrote to fresh files on the same volume and took turns in each comparison. Some loopback tests ran during public HTTPS tests and shared the same machine and disk. Edge used its built-in downloader in a fresh, already-launched profile. aria2 used `--file-allocation=none` and, for multi-connection runs, matching `-x` and `-s` values with `-k1M`. Public HTTPS results reflect this machine, network, and the tested sources.

## Public HTTPS

The Ubuntu ISO was 6.04 GiB. Each run sampled the first 25 seconds and stopped. The table reports the range across two runs per configuration. The downloaded byte count came from the NimGet CLI progress display, curl's output file length, or aria2's last progress report. Both Ubuntu sources published the same SHA-256 in their manifests. [Ubuntu releases](https://releases.ubuntu.com/26.04/) · [JAIST mirror](https://ftp.jaist.ac.jp/pub/Linux/ubuntu-releases/26.04/)

| Ubuntu official source | Throughput (MiB/s) |
| --- | ---: |
| NimGet v1.4.4, 8 connections | 1.97–6.58 |
| NimGet v1.4.4, 1 connection | 3.46–4.73 |
| NimGet v1.4.4, 16 connections | 1.97–2.68 |
| Windows curl | 1.64–2.44 |
| aria2, 8 connections | 2.10–3.52 |

In this sample, opening 16 connections gave lower throughput than a single connection in both rounds. Even with the same source and eight connections, NimGet varied by more than 3× between rounds.

The updated build was also tested through the queue service used by the desktop app, with an isolated database and the default eight connections. Two 25-second samples per source reached 1.80 and 4.29 MiB/s on the official server, and 4.98 and 6.36 MiB/s on the JAIST mirror. A separate one-byte range probe reached the first byte in about 1.08 s on the official server and 0.12–0.19 s on JAIST across IPv4 and IPv6. These samples measure partial bytes and do not establish the time or checksum for a complete ISO download.

The 34.66 MiB Node.js ZIP completed in all three rounds for each NimGet build and its comparison clients. Throughput is in MiB/s. [Node.js v24.11.1 files and checksums](https://nodejs.org/dist/v24.11.1/)

| Client | Baseline median | Retest median |
| --- | ---: | ---: |
| NimGet, 8 connections | 7.00 | 6.61 |
| Windows curl | 9.85 | 12.56 |
| Microsoft Edge | 9.16 | 3.59 |
| aria2, 8 connections | 7.11 | 12.82 |
| aria2, 1 connection | 37.96 | 32.74 |

An exploratory 75.41 MiB Go ZIP comparison had one complete round across all clients. NimGet v1.4.4 reached 8.91 MiB/s, aria2 with eight connections 3.39 MiB/s, Edge 1.71 MiB/s, and curl 0.81 MiB/s. A second round reached 2.44 MiB/s for NimGet and 3.29 MiB/s for aria2. That round was stopped during curl to prioritize the ISO comparison. [Go downloads and checksums](https://go.dev/dl/)

## Controlled loopback

With a 128 MiB fixture, six connections, a 4 MiB/s cap per connection, and a 200 ms delay before each response, evenly sized initial ranges cut the median time from 6.35 s to 5.90 s over five runs. Average throughput over the whole download rose by 7.6%. Recursive halving had assigned four workers 16 MiB each and two workers 32 MiB each. Equal initial ranges reduce the need for follow-up requests when connections run at similar speeds.

| Client | Median throughput (MiB/s) |
| --- | ---: |
| NimGet v1.4.4, 6 connections | 20.17 |
| NimGet updated, 6 connections | 21.69 |
| aria2, 6 connections | 20.27 |

On an uncapped 512 MiB loopback fixture, five runs gave medians of 403.46 MiB/s for NimGet v1.4.4, 420.84 MiB/s for the updated build, 152.93 MiB/s for Windows curl, 139.78 MiB/s for Edge, and 79.01 MiB/s for aria2. NimGet and aria2 used eight connections. The two NimGet builds start with the same range sizes in this test, so the difference between their medians provides no evidence of an improvement from range balancing at eight connections.

With eight connections, a 128 MiB fixture, a 4 MiB/s cap per connection, and no extra response delay, five runs gave medians of 30.08 MiB/s for v1.4.4, 29.11 MiB/s for the updated build, and 30.97 MiB/s for aria2. The updated build reached 94% of aria2 in this Windows test. One instrumented run spent about 3.99 s receiving and writing the ranges and another 151 ms persisting the final checkpoint. Trying to eliminate a subsequent duplicate file sync produced no median speedup across five runs.

## Reproduce the controlled comparison

Build both NimGet versions and the fixture server. Run these commands from the repository root in PowerShell.

```powershell
$oldSource = Join-Path $env:TEMP 'nimget-v1.4.4-source'
git worktree add --detach $oldSource v1.4.4
Push-Location $oldSource
go build -o "$env:TEMP\nimget-v1.4.4.exe" ./cmd/nimget
Pop-Location
go build -o "$env:TEMP\nimget-current.exe" ./cmd/nimget
go build -o "$env:TEMP\nimget-bench.exe" ./tools/bench
& "$env:TEMP\nimget-bench.exe" -serve 128 -rate 4 -latency 200ms
```

Keep the fixture server running. It prints a loopback URL and SHA-256. In a second terminal, set `$url` to that URL and run the clients in rotating order. Use fresh filenames for each of five rounds and hash each result afterward.

```powershell
Measure-Command { & "$env:TEMP\nimget-v1.4.4.exe" download --connections 6 $url "$env:TEMP\old.bin" }
Measure-Command { & "$env:TEMP\nimget-current.exe" download --connections 6 $url "$env:TEMP\current.bin" }
Measure-Command { aria2c --no-conf -x6 -s6 -k1M --file-allocation=none -d $env:TEMP -o aria.bin $url }
Get-FileHash "$env:TEMP\old.bin","$env:TEMP\current.bin","$env:TEMP\aria.bin"
```

For uncapped loopback, start the fixture with `-serve 512 -rate 0`. Use eight connections for NimGet and aria2, and time `curl.exe -fLsS -o curl.bin $url` as the single-connection baseline. The standalone `tools/bench` comparison reports the fastest of three runs, and the CI gate checks peak RSS on Unix.

For a public complete-file comparison, repeat the command pattern with eight connections and the [Node.js Windows x64 ZIP](https://nodejs.org/dist/v24.11.1/node-v24.11.1-win-x64.zip). For the single-connection aria2 baseline, omit `-x`, `-s`, and `-k`. Compare every result with the published SHA-256.
