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
