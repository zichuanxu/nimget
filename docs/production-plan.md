# Production readiness plan

Status: product scope and test boundaries synthesized in [the production specification](production-spec.md), published as [GitHub issue #1](https://github.com/zichuanxu/nimget/issues/1) with the `ready-for-agent` label. Implementation has not started.

## Confirmed scope (2026-11-16)

- Prioritize Windows 10/11 with Chrome and Edge. Preserve existing macOS and Linux capabilities; defer other browser integrations.
- Cover HTTP(S) files, authenticated downloads, reliable resume, queue scheduling, browser handoff, desktop usability, and existing HLS functionality.
- Exclude DRM and payment restriction bypass from this scope.
- Keep expenditure at zero. Distribute installers through GitHub Releases. This supersedes the source-only distribution decision in DESIGN.md for the planned release; packaging is not yet implemented.
- Default to an explicit browser context-menu action. Automatic capture is opt-in, with site and file-type exclusions. Cancel the browser download only after durable task acceptance; otherwise preserve the browser download.
- NimGet has no account or login system. Support on-demand transfer of relevant website cookies for authenticated downloads. Require initial desktop confirmation of extension pairing and support revocation.
- Preserve the current React/Wails architecture and visual style. Improve bulk operations, accessibility, failure recovery, task details, and browser connection visibility.

## Decisions still open

- Exact interfaces, schemas, packaging technology, and implementation schedule remain implementation details. The user confirmed service/API integration tests, local HTTP fault fixtures, and focused browser, desktop, and installer acceptance as the test boundaries.

## Proposed implementation sequence and acceptance

1. Repair credential boundaries, partial-file safety, signed-URL renewal, and batch retry behavior. Add regression coverage and run the relevant Go tests.
2. Implement browser pairing and revocation plus a Chrome/Edge extension: explicit handoff, opt-in capture, exclusion rules, scoped website credentials, and offline fallback. Verify unauthorized access is denied and accepted tasks survive restart.
3. Improve existing-task editing, multi-selection, failure recovery, structured site settings, and dialog keyboard behavior while retaining the current visual style. Build the frontend and exercise the desktop workflows.
4. Restore Windows installer packaging and GitHub Release automation with checksums and installation instructions. Keep extension distribution available as a release archive with unpacked-install instructions; store publication is outside this zero-spend milestone.
5. Run the available full tests and build checks. Report platform, browser, installer, and external-service checks that could not be exercised rather than claiming production parity without evidence.

Actual publication to GitHub and execution of release workflows are separate from preparing and validating their repository changes.

## Initial evidence

These are code-review findings, not claims of reproduced exploits or completed fixes.

| Priority | Finding | Evidence | Required verification |
| --- | --- | --- | --- |
| High | Sensitive-header boundaries compare hostname, omitting scheme and port | internal/engine/transport.go; internal/hls/fetch.go | Redirect and HLS tests across origin boundaries |
| High | Partial-file truncation follows preexisting filesystem links | internal/engine/single.go; internal/hls/hls.go | Link safety tests, including platform behavior |
| High | Browser extension and pairing are absent | DESIGN.md section 6; internal/api/http.go | End-to-end handoff, authorization, and offline fallback |
| High | Fresh cURL reauthentication replaces headers but not an expired signed URL | app/desktop.go Reauthenticate | Renew an expired signed URL without corrupting resume state |
| Medium | Batch add can partially succeed and duplicate successful entries on retry | app/frontend/src/AddDialog.tsx | Partial failure and retry scenario |
| Medium | Existing task settings cannot be edited in the GUI | app/frontend/src/App.tsx; app/desktop.go Update | Priority, connection, and speed-limit editing |
| Medium | Single selection, no configurable sorting or virtualized task list | app/frontend/src/App.tsx | Bulk actions and large-queue responsiveness |
| Medium | Modal focus containment, restoration, and accessible naming are missing | app/frontend/src/ui.tsx | Keyboard and assistive-technology checks |
| Medium | Per-site settings require raw JSON editing | app/frontend/src/SettingsDialog.tsx | Structured fields with validation |
| Medium | HLS alternate audio/subtitle renditions are omitted | internal/hls/playlist.go; docs/usage.md | Explicitly communicate unsupported media combinations |
| High | Installer/release automation retired; desktop manual acceptance remains pending | DESIGN.md sections 10 and 11; .github/workflows/ci.yml | Clean-machine installation and desktop acceptance |

## Validation performed

- `go test ./internal/engine ./internal/hls` passed on the current Windows workspace.
- No full-suite, race, desktop runtime, browser integration, or installer acceptance run has been completed in this discovery session.
- Existing tests do not establish IDM performance or compatibility parity.
