# Using nimget

The CLI, the background service, its settings and HTTP API, and the security model.

## Direct downloader

The `download` command runs the engine without the service:

```bash
./nimget download \
  --connections 8 \
  --checksum 'sha256:<64-character-hex-digest>' \
  'https://example.com/large-file.bin' \
  './large-file.bin'
```

| Flag | Default | Meaning |
| --- | --- | --- |
| `--connections` | `8` | Parallel connections, at most 32. |
| `--min-split-mib` | `1` | Smallest range handed to one connection. |
| `--stall-timeout` | `30s` | Reconnect a connection that receives nothing for this long. |
| `--attempts` | `5` | Consecutive failed requests without progress before giving up. |
| `--checksum` | none | `algo:hex` with `sha256`, `sha512`, `sha1`, or `md5`. |
| `--resume-key` | the URL | Stable identity for expiring signed URLs. |
| `--header` | none | Extra request header; repeatable. |
| `--speed-limit` | `0` | Bytes per second, with `K`, `M`, or `G`; `0` is unlimited. |
| `--proxy` | `system` | `system`, `none`, or a `http://`, `https://`, or `socks5://` proxy URL. |
| `--overwrite` | off | Replace an existing destination. |

```bash
./nimget download --header 'Authorization: Bearer TOKEN' URL OUTPUT
./nimget download --resume-key 'bucket/object/version-42' SIGNED_URL OUTPUT
```

Completed files are marked as downloaded from the internet (the `com.apple.quarantine` attribute on macOS, a `Zone.Identifier` stream on Windows), so Gatekeeper and SmartScreen check downloaded programs as they would a browser download.

While a transfer is active the engine keeps `OUTPUT.part`, `OUTPUT.part.meta`, and `OUTPUT.lock` (for HLS, finished segments in `OUTPUT.hls/`). The lock is an OS advisory lock, so a crashed or killed process never leaves a lock that blocks the next run. The engine checks free space and preallocates the file before downloading. If a partial path belongs to a different task or its ownership cannot be verified, NimGet leaves it in place and asks you to move or rename it before retrying.

When parallel download and resume apply:

| Server offers | Parallel | Resume after a restart |
| --- | --- | --- |
| Ranges, known size, strong ETag | yes | yes |
| Ranges, known size, `Last-Modified` only | yes | yes |
| Ranges, known size, no validator | yes | no, restarts from zero |
| No ranges, or unknown size | single connection | no, restarts from zero |

### Streaming media

NimGet downloads on-demand HLS from a `.m3u8` URL or HLS content type, including clear streams and AES-128 with identity keys. It assembles MPEG-TS segments or fragmented MP4 with initialization sections. A master playlist selects its highest-bandwidth variant, breaking ties by resolution. Sensitive request headers stay within the playlist's security origin.

NimGet fails the task if the selected variant supplies audio, video, or subtitles through separate playlists. Live HLS, I-frame-only playlists, DASH manifests, and encryption such as SAMPLE-AES also produce an unsupported-media failure. Check **Download details** for guidance or inspect the task error through the local API. Try a direct video file or an on-demand HLS link with all required tracks in the same stream.

For finished HLS downloads named `.ts`, the desktop app offers **Convert to MP4** when ffmpeg is installed on PATH or configured in settings. This can also help players that rely on the extension open an fMP4 download initially named `.ts`.

## Background service and CLI client

```bash
./nimget service \
  --listen 127.0.0.1:51000 \
  --download-root "$HOME/Downloads"
```

| Flag | Default | Meaning |
| --- | --- | --- |
| `--listen` | `127.0.0.1:51000` | Loopback address; non-loopback addresses are rejected. |
| `--database` | `<config dir>/nimget/nimget.db` | SQLite queue. |
| `--download-root` | `~/Downloads` | Directory downloads may be written under. Repeatable. |
| `--log-level` | `info` | JSON logs on stderr: `debug`, `info`, `warn`, `error`. |
| `--token-file` | `<config dir>/nimget/token` | API token, created on first start with owner-only permissions. |

`<config dir>` is `~/Library/Application Support` on macOS, `%AppData%` on Windows, and `$XDG_CONFIG_HOME` or `~/.config` on Linux.

Client commands read the same token file and talk to `http://127.0.0.1:51000` unless `--address` or `--token-file` say otherwise:

```bash
./nimget add 'https://example.com/file.bin'                 # named by the server
./nimget add 'https://example.com/file.bin' ~/Downloads/f.bin --priority high --speed-limit 2M
./nimget add URL --dir ~/Downloads/isos --connections 16 --header 'Cookie: session=...'
./nimget list
./nimget set <id> --priority low --speed-limit 0
./nimget pause <id>
./nimget resume <id>
./nimget retry <id>
./nimget delete <id> --files
./nimget settings get > settings.json
./nimget settings set settings.json
./nimget add --batch 'https://example.com/photos/img[001-120].jpg' --dir ~/Downloads/photos
./nimget export queue-summary.json                            # share unfinished queue progress
./nimget export --include-source-urls queue-backup.json       # save exact source links
./nimget import queue-backup.json                             # or a text file with one URL per line
```

**Export queue summary** in the desktop menu and `nimget export` save unfinished-task states, progress, and numeric options for sharing. A summary contains no source or destination strings. For import, choose a source URL backup or supply a list of download links.

**Back up queue with source URLs** and `nimget export --include-source-urls FILE` save exact links, destinations, and request options in plain text. Links and destinations can contain credentials. Request headers, website sessions, and partial bytes are omitted. Protected tasks may need refreshed links or a new website session. Destinations must be inside an allowed download root on the importing machine. Older queue exports may have omitted parts of source links. Review those links, then confirm the desktop prompt or use `nimget import --allow-legacy-urls FILE` to import the links as saved.

Batch patterns expand left to right: `[1-10]`, zero-padded `[001-120]`, stepped `[0-100:5]`, letters `[a-z]`, and alternatives `{cd,dvd}`, up to 10,000 URLs.

Without `OUTPUT`, the file name comes from `Content-Disposition` (including RFC 5987 `filename*`), then the URL path, then the host. It is sanitized (no path separators, `..`, control characters, or Windows reserved names; at most 200 bytes) and placed in `--dir`, or in a category folder under the first download root: `Video`, `Music`, `Documents`, `Compressed`, or `Programs` by extension, the root itself otherwise. A name that is taken on disk or by another queued download becomes `name (1).ext`.

Relative paths are resolved against the client's working directory. On restart, the service resumes downloads that were running when it stopped, including after a crash.

### Magnet downloads

Paste a public, tracked single-file BitTorrent v1 magnet in **New Download** or pass the quoted link to `nimget add`. Use **Save to** or `--dir` to pick the parent directory. The verified torrent metadata supplies the file name.

The task enters the queue before peer discovery. Once NimGet retrieves the file list, progress counts verified pieces. NimGet adds the final file after verification and uses a numbered suffix if that name is already taken. A magnet stays queued while a proxy is active.

### Settings

`nimget settings set` replaces the whole document, so edit the output of `settings get`:

```json
{
  "maxConcurrent": 3,
  "connections": 8,
  "hostConnections": 16,
  "speedLimit": 0,
  "torrentUploadLimit": 65536,
  "schedule": {
    "start": "01:00",
    "stop": "07:00",
    "speedRules": [{ "from": "09:00", "to": "18:00", "limit": 1048576 }]
  },
  "proxy": { "mode": "system" },
  "sites": [
    { "host": "example.com", "connections": 4, "hostConnections": 4,
      "headers": { "Referer": "https://example.com/" }, "username": "me", "password": "secret" }
  ]
}
```

| Field | Meaning |
| --- | --- |
| `maxConcurrent` | Downloads running at once, 1–64. |
| `connections` | Default connections per HTTP-based download, 1–32. |
| `hostConnections` | HTTP-based connections to one host, 1–64. A download takes what its host has left and waits if none are available. |
| `speedLimit` | Shared download cap for HTTP-based and magnet downloads in bytes per second. Use `0` for unlimited. |
| `torrentUploadLimit` | Shared upload cap for active magnet downloads in bytes per second. The default is 65,536 (64 KiB/s). Supply a positive value to change it. |
| `schedule.start`, `schedule.stop` | Local `HH:MM` window in which the queue runs, wrapping past midnight. Outside it running downloads return to the queue. Leave both empty to run all day. |
| `schedule.speedRules` | Time-of-day global limits; the first matching rule wins over `speedLimit`. |
| `proxy.mode` | `system` (macOS and Windows settings, else `HTTP_PROXY`/`HTTPS_PROXY`), `none`, or `manual` with `proxy.url`. Loopback addresses never use a proxy. |
| `sites` | HTTP-based download overrides for a host and its subdomains: connections, host cap, extra headers, and basic-auth credentials. |

On the desktop, open **Settings → Downloads** to set the total download limit and BitTorrent upload limit. Magnet downloads can upload verified pieces while downloading. They disconnect when paused, completed, or stopped by service shutdown.

Set per-download priority, connections, and speed limit with `add` or `set`, or open **Download details** from the task's info button or menu in the desktop app. Priority affects which queued task starts next. Connection changes apply on the next attempt and still respect the server's support and the site's connection cap. A new speed limit applies to subsequent reads on a running download. An existing limiter wait may finish at the previous rate, and the total speed limit still applies. In the desktop app, leave connections blank to use the site or app default and speed limit blank to clear the task limit.

**Download details** shows the destination, progress, current status, and recovery guidance for failed tasks. Retry after fixing disk-space, folder permission, or network problems. If the website requires a fresh link or session, use **Refresh download link** on the same task.

Site passwords are shown as `********` by `settings get`; sending that value back keeps the stored password.

Download headers (such as cookies) and site credentials are encrypted in the database with AES-GCM under a key kept in the OS keyring (Keychain, Windows Credential Manager, or the Secret Service). Without a keyring, as on a headless server, the key lives in memory only: after a restart those values are gone and a download that needs them fails with 401 or 403 until its headers are supplied again.

## HTTP API

Every endpoint except `/v1/ping` requires `Authorization: Bearer <token>`. Commands other than `GET` require `Content-Type: application/json`.

| Method | Path | Behavior |
| --- | --- | --- |
| `GET` | `/v1/ping` | `{"status":"ok"}` |
| `GET` | `/v1/downloads` | Lists downloads with live progress. |
| `POST` | `/v1/downloads` | Body `{"url":"...","destination":"/absolute/path"}`, or `"directory"` or neither for a server-named file; optional `priority` (`-1`, `0`, `1`), `connections`, `speedLimit`, `checksum`, `headers`. Returns `202` with the queued item. |
| `POST` | `/v1/downloads/{id}:pause` | Pauses a queued or running download. `204` |
| `POST` | `/v1/downloads/{id}:resume` | Requeues a paused download. `204` |
| `POST` | `/v1/downloads/{id}:retry` | Requeues a failed download. `204` |
| `POST` | `/v1/downloads/{id}:restart` | Discards partial data and requeues a failed download with `recovery: "restart-required"`. `204` |
| `PATCH` | `/v1/downloads/{id}` | Updates `priority`, `connections`, `speedLimit`, or `headers`. Supplying `url` refreshes the source and requeues a stopped failed or paused download. `204` |
| `DELETE` | `/v1/downloads/{id}` | Deletes a stopped download; `?files=true` also removes partial data and, for completed downloads, the file. `204` |
| `GET` | `/v1/settings` | The settings document. |
| `PUT` | `/v1/settings` | Replaces the settings; returns them, or `422` with every validation error. |
| `GET` | `/v1/events` | `text/event-stream`; also accepts `?token=` because `EventSource` cannot send headers. |

Errors use `{"error":{"code":"...","message":"..."}}`: `404` for an unknown ID, `409` for a command the current status forbids (for example pausing a completed download, or deleting one that is still stopping), and `422` for an invalid URL, a destination outside the download roots, or a destination another download already uses.

Download recovery keeps the task ID and destination. Include fresh `headers` alongside the new `url` if the source requires them. A URL replacement clears the previous request headers. The desktop **Refresh download link** action accepts either a pasted URL or the browser's **Copy as cURL** command.

For HTTP partial data, NimGet checks byte-range support, size, and a matching strong ETag. If the final resource URL changes, including its query string, NimGet downloads the saved ranges again to compare the bytes before resuming. This adds verification traffic. Links that resolve to the same final resource URL can resume without that extra transfer.

An incompatible checkpoint, mismatched saved bytes, or partial HLS data whose segment contents cannot be validated sets `recovery: "restart-required"`. The saved data stays available while you try another link. Choose **Restart from beginning** to discard it and download from the current source. Ordinary API retry returns `409 RESTART_REQUIRED` in this state.

The event stream starts with one `snapshot` event holding every download, followed by `updated`, `progress`, and `deleted` deltas:

```text
id: 12
event: progress
data: {"id":"...","status":"running","completed":123,"total":456,...}

```

A client that falls behind is disconnected and should reconnect for a fresh snapshot; missed events are not replayed.

## Security model

- The service listens on loopback only and requires the bearer token on every command.
- The service accepts only `127.0.0.1`, `localhost`, and `::1` as the request host. CLI commands reject browser origins. The dedicated [extension pairing routes](browser-pairing.md#loopback-api) accept only the NimGet extension origin.
- Destinations must resolve, after following symlinks, inside a `--download-root`, and never inside known autostart directories.
- Logs never contain request headers, and URLs are logged with credentials and signature parameters redacted. Do not share the token file.
