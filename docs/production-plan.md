# Production readiness plan

The product scope and test boundaries appear in the [production specification](production-spec.md) and [GitHub issue #1](https://github.com/amemiya02/nimget/issues/1). This plan records the implemented scope and the remaining acceptance work. See the [Windows release guide](windows-release.md) for the implemented packaging and validation commands.

## Confirmed scope (2026-11-16)

- Runtime acceptance uses Windows 11; the user explicitly waived Windows 10 runtime testing during implementation. Existing Windows 10 compatibility targeting may remain, without a claim that it was tested.
- Prioritize Windows 11 with Chrome and Edge. Preserve existing macOS and Linux capabilities; defer other browser integrations.
- Cover HTTP(S) files, authenticated downloads, reliable resume, queue scheduling, browser handoff, desktop usability, and existing HLS functionality.
- Exclude DRM and payment restriction bypass from this scope.
- Keep expenditure at zero. Prepare the unsigned Windows installer and matching extension archive for GitHub Releases.
- Default to an explicit browser context-menu action. Automatic capture is opt-in, with site and file-type exclusions. Cancel the browser download only after durable task acceptance; otherwise preserve the browser download.
- NimGet has no account or login system. Support on-demand transfer of relevant website cookies for authenticated downloads. Require initial desktop confirmation of extension pairing and support revocation.
- Preserve the current React/Wails architecture and visual style. Improve bulk operations, accessibility, failure recovery, task details, and browser connection visibility.

## Test boundaries

- The user confirmed service/API integration tests, local HTTP fault fixtures, and focused browser, desktop, and installer acceptance as the test boundaries.

## Implemented

- Exact-origin credential handling, safe partial files and source continuity, signed-URL renewal, and retry of only rejected batch entries.
- Revocable browser pairing and Chrome/Edge integration with manual handoff, automatic capture, exclusions, optional same-origin website sessions, and browser fallback.
- Task editing, bulk actions, a paginated queue, structured site settings, and keyboard navigation in the existing desktop interface.
- A Windows installer, a matching extension archive, checksums, and reproducible release preparation. The unsigned candidate remains unpublished.

## Acceptance status

See the [Windows acceptance record](windows-acceptance.md) for historical installer and archive evidence, review regressions, and the boundaries of each test run. Local production preview uses a simulated Wails transport. Native installer checks use the actual Windows desktop and WebView2.

A clean Windows 11 host without developer tooling, missing-WebView2 behavior, internet-download security prompts, and full native assistive-technology coverage remain to be verified. The local WSL performance run missed the unchanged CPU threshold. The hosted matrix and performance gate must be evaluated against the final integration revision. Windows 10 runtime was waived and IDM parity remains unverified.
