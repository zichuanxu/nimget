# Running NimGet from source

NimGet is distributed as source code. The desktop app runs on macOS and Windows; the CLI and headless service also run on Linux.

## Prerequisites

- Git and Go 1.25+ for the CLI and service.
- Node.js 22.12+ and npm for the desktop frontend.
- macOS desktop: Xcode Command Line Tools (`xcode-select --install`).
- Windows desktop: Microsoft Edge WebView2 Runtime. See the [Wails platform requirements](https://v3.wails.io/quick-start/installation/) for setup details.

## Get the source

```bash
git clone https://github.com/zichuanxu/nimget.git
cd nimget
```

Run CLI commands from the repository root. The desktop app lives in `app/`, a separate Go module that uses the root module through a local `replace` directive.

## CLI and service

For a single download:

```bash
go run ./cmd/nimget download https://example.com/file.iso ./file.iso
```

For a queue, start the service and leave its terminal open:

```bash
go run ./cmd/nimget service
```

In another terminal at the repository root:

```bash
go run ./cmd/nimget add https://example.com/file.iso
go run ./cmd/nimget list
```

You can also build a local CLI:

```bash
go build ./cmd/nimget
./nimget version
```

On Windows the output is `nimget.exe`; run `.\nimget.exe version` in PowerShell. The examples in [usage.md](usage.md) use this local binary.

On Linux servers without a desktop keyring, cookies and passwords are kept in memory only and must be supplied again after a restart.

## Desktop app

From the repository root:

```bash
cd app/frontend
npm ci
npm run build
cd ..
go run .
```

The frontend must be built before starting the app because Go embeds `app/frontend/dist`. The app includes the service, so CLI commands can talk to it while it is open. Quit any other NimGet app or service before starting one: both use port 51000 and a single-instance lock.

Closing the window keeps downloads running. Choose **Quit NimGet** from the tray or menu bar to stop the app.

### UI live reload

First build the frontend once using the steps above. Then run this in a terminal at `app/frontend`:

```bash
npm run dev
```

In a second terminal at `app`, on macOS:

```bash
FRONTEND_DEVSERVER_URL=http://127.0.0.1:9245 go run .
```

Or in Windows PowerShell:

```powershell
$env:FRONTEND_DEVSERVER_URL = "http://127.0.0.1:9245"
go run .
```

## Update your checkout

Stop the running app or service. From the repository root:

```bash
git pull --ff-only
```

Restart the CLI or service with `go run ./cmd/nimget ...`. For the desktop app, repeat `npm ci`, `npm run build`, and `go run .` from the directories shown above. Rebuild with `go build ./cmd/nimget` if you use a local CLI binary.

## Verify changes

Run `go test ./...` at the repository root and again in `app/`. The frontend is checked by `npm run build`. See [desktop-checklist.md](desktop-checklist.md) for manual desktop checks.
