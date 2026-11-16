# Desktop interaction tests

From `app/frontend`, run:

```sh
npm ci
npx playwright install chromium
npm test
```

Set `NIMGET_TEST_BROWSER` to `msedge` or `chrome` to use that installed browser without downloading Chromium. For example, run `$env:NIMGET_TEST_BROWSER = "msedge"` before `npm test` in PowerShell. Use `npm test -- --grep "<name>"` to select an interaction.

Set `NIMGET_TEST_PORT` to a free loopback port when testing multiple worktrees concurrently. The default is `19245`.

Playwright starts the frontend with Vite on loopback. In a `*.spec.ts` file under this directory, call `mockDesktop(page, { MethodName: handler })` before `page.goto("/")`. The helper supplies default startup responses and intercepts the Wails runtime transport. Tests can override individual Desktop methods while exercising the real frontend and generated bindings. Prefer queries by visible names and roles.

This harness covers desktop frontend interactions in a browser. Use service integration tests for persistence and use the native app for platform-specific behavior.

## Large persisted queue benchmark

Run this from the repository root on Windows with Chrome installed. Use one browser worker and the production assets for each comparison.

```powershell
$env:NIMGET_QUEUE_FIXTURE = Join-Path $env:TEMP nimget-large-queue.json
go test ./internal/service -run '^TestLargePersistedQueueTracksProgress$' -count=1 -v
Push-Location app/frontend
npm.cmd ci
npm.cmd run build
$env:NIMGET_TEST_BROWSER = "chrome"
$env:NIMGET_TEST_PREVIEW = "1"
npm.cmd test -- --workers=1 --repeat-each=3 --grep "measures search"
Pop-Location
```

The Go test seeds SQLite with 2,000 download tasks, closes the store, and opens the service on an ephemeral loopback port. Four downloads use a controlled runner that reports 64 KiB every 100 ms. The authenticated list must return every seeded ID and live progress at least as recent as the updates already observed. The other tasks are completed, failed, or paused. The exported fixture contains the actual API list response.

Playwright serves the production assets and stubs the Wails transport. It replays 128 progress events from the four active tasks in round-robin order, scheduled 25 ms apart. During playback it searches by name, applies a broader case-insensitive search, clears the query, and switches between Failed, Active, and All downloads. The test checks result counts and the final progress of the first task. Regular browser tests also cover keyboard paging, selection and actions across pages, focus restoration, status changes, and events that arrive during resync.

### Recorded comparison for issue #15

Baseline commit `1451b0ea4c548ee0cd0f9d3068eb4aa5df42bf8f`. Each side ran three times using headless Chrome 157.0.8103.51 with Playwright 1.65.1 and a 1280 × 720 viewport. The host ran Windows 11 build 26200 on an Intel Core i9-13900H with 32 GiB RAM, Go 1.27.4, and Node 26.13.1.

| Measurement | Baseline | With paging and memoized rows |
| --- | ---: | ---: |
| Rows mounted after opening the queue | 2,000 | 50 |
| DOM elements after opening the queue | 76,705 | 2,024 |
| Queue ready, median | 1,775 ms | 240 ms |
| Time to deliver 128 updates, range | 18.43–21.56 s | 3.22–3.31 s |
| Main-thread script time during workload, median | 15,518 ms | 240 ms |
| Long tasks per run, range | 128–130 | 0 |
| Longest sampled interaction per run, median | 613 ms | 85 ms |

Queue readiness includes navigation, transport stub delivery, and Playwright's first successful visibility checks. Interaction samples measure an input or click to the second animation frame. When actions share a frame, the benchmark keeps the latest sample. Under load, the JavaScript timer waits for the main thread, so a delayed interval extends the fixed-count workload. The timings cover the local browser harness with controlled progress and are specific to this desktop and workload. Native WebView timing and actual transfer speeds require separate measurements.
