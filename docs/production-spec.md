# NimGet: Windows production readiness and browser integration

## Problem Statement

NimGet already offers a substantial download engine and desktop application, but a Windows user cannot yet treat it as an everyday browser-integrated download manager. Browser downloads require manual link or cURL transfer, installation requires developer tooling, and failures such as expired signed URLs or partially successful batch submissions are difficult to recover from correctly.

Existing credential forwarding and partial-file handling also require hardening before introducing browser-supplied website sessions. The desktop lacks efficient bulk management, accessible modal interactions, and straightforward editing of existing download tasks. Passing engine tests does not establish end-to-end reliability or parity with IDM.

The target is a practical first production milestone for Windows 10/11 and Chrome/Edge, preserving existing macOS desktop and Linux CLI/service capabilities. NimGet remains free, requires no NimGet account, and incurs no paid infrastructure or distribution costs.

## Solution

Provide an installable Windows download manager with an accompanying Chrome/Edge extension. Users can explicitly send a link to NimGet or enable automatic capture with exclusions. Extension pairing is approved in the desktop application and can be revoked. A website session is transferred only as needed for a user-initiated download; it is not a NimGet login.

Automatic capture must preserve the browser's ability to finish a download whenever handoff cannot be confirmed. Downloads must remain recoverable after interruption without credential leakage or unintended file modification. Improve the current desktop interactions and visual style rather than replacing the application architecture or conducting a visual redesign.

Distribute Windows installers and extension artifacts through GitHub Releases with installation instructions and checksums. Browser-store publication and paid signing are outside this milestone.

## User Stories

1. As a Windows user, I want to install NimGet without Go or Node.js, so that I can use it without a development environment.
2. As a Windows user, I want installation instructions to explain prerequisites and unsigned-package prompts accurately, so that installation behavior is understandable.
3. As a user, I want NimGet to work without registration or a NimGet account, so that downloading remains a local workflow.
4. As a release user, I want versioned artifacts and checksums, so that I can identify and verify the files I download.
5. As a release user, I want a documented update process that preserves my queue and settings, so that installing a newer version does not lose my work.
6. As a user, I want clear uninstall behavior for downloaded files and application data, so that removing NimGet does not unexpectedly erase my files.
7. As a Chrome or Edge user, I want extension installation instructions, so that I can enable browser integration from the release artifacts.
8. As a browser user, I want a right-click action to send a link to NimGet, so that manual handoff is quick and explicit.
9. As a new extension user, I want automatic capture disabled initially, so that browser behavior changes only when I choose it.
10. As a browser user, I want to opt into automatic capture, so that eligible downloads reach NimGet without repeated manual actions.
11. As a browser user, I want website exclusions, so that selected websites continue using the browser downloader.
12. As a browser user, I want file-type exclusions, so that files I do not want managed by NimGet remain in the browser.
13. As a browser user, I want an excluded or unsupported download to remain available in the browser, so that integration does not block it.
14. As a browser user, I want automatic capture to cancel the browser download only after durable acceptance by NimGet, so that a failed handoff does not lose my download.
15. As a browser user, I want a clear fallback when NimGet is offline or rejects a task, so that I can continue downloading in the browser.
16. As a browser user, I want repeated delivery of the same handoff to avoid duplicate tasks, so that connection retries do not duplicate downloads.
17. As a browser user, I want ambiguous handoff results to be reported honestly, so that I can resolve a possible duplicate without losing the original download.
18. As a desktop user, I want to approve initial extension pairing, so that an unrelated extension cannot silently control NimGet.
19. As a desktop user, I want to see and revoke paired extensions, so that I retain control over browser access.
20. As a browser user, I want connection and pairing status to be visible, so that I understand why a handoff is unavailable.
21. As a user already signed into a website, I want the necessary website session information included with my requested download, so that protected files can be retrieved without a second login workflow.
22. As a privacy-conscious user, I want website access permissions explained and requested only where needed, so that I understand what the extension can read.
23. As a privacy-conscious user, I want to deny website-session access and still download public files, so that integration does not require broad cookie access.
24. As a privacy-conscious user, I want credentials excluded from ordinary logs, errors, and shared exports, so that troubleshooting does not disclose them.
25. As a user, I want website credentials constrained to the intended security origin, so that redirects or media requests do not expose them to another service.
26. As a user, I want a visible warning when website credentials cannot be retained securely, so that I know protected downloads may require renewed authorization after restart.
27. As a downloading user, I want an interrupted task to resume where safe, so that outages and application restarts do not waste completed work.
28. As a downloading user, I want a changed source detected before old and new bytes are combined, so that a resumed file is not silently corrupted.
29. As a downloading user, I want a refreshed signed URL to repair an expired download task, so that I do not have to recreate the task unnecessarily.
30. As a downloading user, I want a clear restart requirement when source continuity cannot be established, so that unsafe resume is not presented as successful recovery.
31. As a user, I want partial-file collisions or filesystem links handled safely, so that downloading cannot truncate unrelated files.
32. As a user, I want actionable failures for disk-space, permission, network, and authorization problems, so that I can choose an appropriate recovery action.
33. As a user, I want existing queue priorities, schedules, host limits, and speed limits preserved, so that browser integration respects my download preferences.
34. As a user, I want to edit an existing task's priority, connection count, and speed limit, so that I can adjust work without recreating it.
35. As a user with many tasks, I want to select multiple tasks and apply appropriate actions, so that queue management is efficient.
36. As a user performing bulk actions, I want per-task outcomes and explicit deletion behavior, so that partial failure does not hide what happened.
37. As a user, I want task details to show progress, destination, status, limits, and recovery information, so that I can understand an individual download.
38. As a user, I want search and filtering to remain usable with a large queue, so that accumulated download history does not make the application unresponsive.
39. As a batch user, I want submission results to distinguish accepted and rejected entries, so that partial success is understandable.
40. As a batch user, I want retry to submit only unresolved entries, so that already accepted downloads are not added again.
41. As a keyboard user, I want dialogs to have an accessible name, contain focus, and restore focus when closed, so that I can navigate reliably.
42. As a keyboard or assistive-technology user, I want understandable task selection and action states, so that mouse input is not required for core workflows.
43. As a user, I want structured per-site settings with validation, so that common configuration does not require editing raw JSON.
44. As an existing user, I want current themes, language support, and visual conventions preserved, so that the improvements remain familiar.
45. As an HLS user, I want supported on-demand media downloads and existing conversion behavior preserved, so that browser work does not regress media support.
46. As an HLS user, I want unsupported separate audio, subtitles, or stream types made explicit, so that a successful task does not imply unsupported media completeness.
47. As a macOS or Linux user, I want existing supported functionality to keep working, so that Windows prioritization does not break my workflows.
48. As a maintainer, I want release validation and its limitations recorded, so that production claims reflect actual evidence.

## Implementation Decisions

- Retain the shared Go service, queue manager, persistent store, download engine, and existing Wails/React desktop architecture. Do not rewrite working segmentation or scheduling subsystems as part of this milestone.
- Prioritize Windows 10/11 desktop acceptance and Chrome/Edge integration. Preserve current platform tiers for other supported surfaces.
- NimGet has no account system, cloud authentication, or hosted control plane. Website sessions and extension pairing are distinct concepts.
- Keep the existing desktop appearance, themes, and language support. Changes focus on interaction completeness, recovery, accessibility, and status visibility.
- Extend the local service boundary intentionally for browser integration. Preserve loopback binding, host validation, authenticated operations, and rejection of untrusted browser origins. Do not enable unrestricted CORS or give ordinary web pages access to the service.
- Pairing requires an explicit initial desktop approval and supports revocation. The extension must not receive unrestricted administrative access merely to submit downloads. Exact wire formats, expiry values, and pairing persistence are implementation details to document with the resulting contract, not decisions already settled by the interview.
- Transfer website cookies and necessary request context only for the user-requested target. Do not periodically synchronize a browser-wide cookie store. Declined permissions must produce a usable public-download path and an explanation for protected-download failures.
- Reuse secure credential storage capabilities. Do not expose credentials through normal diagnostics or exports. If secure persistence is unavailable, explain the resulting restart limitation to the user.
- Treat scheme, normalized hostname, and effective port as the security origin when deciding whether sensitive request headers may be forwarded. Cover both regular HTTP redirects and HLS resource requests. Preserve nonsensitive transport headers only where appropriate.
- Harden partial-file creation and reopening across ordinary and HLS downloads. Existing links or unsafe file identities must not cause unrelated-file truncation. Preserve destination confinement and downloaded-file quarantine behavior.
- Manual handoff is the default extension behavior. Automatic capture is opt-in and respects website and file-type exclusions. Capture only requests that can be safely represented by the supported downloader; leave unsupported request forms in the browser with useful feedback.
- A successful handoff response means the task has been persisted, not merely received in memory. Browser cancellation must follow that acknowledgement. Persistence failure, denial, timeout, or an unavailable desktop must not trigger cancellation.
- Handoff delivery needs a stable request identity so retries can return the original accepted task rather than create duplicates. An acknowledgement lost after commit is an ambiguous outcome: preserve the browser download and reconcile the handoff identity where possible. Do not promise cross-application exactly-once downloading.
- Browser cancellation failure must be visible. Retain the accepted NimGet task and report that the browser may also be downloading; do not claim exclusive ownership without evidence.
- Extend task recovery to accept a refreshed source URL as well as updated request headers. Revalidate resource continuity using existing engine rules before reusing partial data. Do not reuse bytes solely because the filename or hostname matches.
- Keep batch submission results per entry. Preserve successful outcomes when another entry fails, and make retries target unresolved entries. Do not require an unrelated global ban on intentionally downloading a URL twice.
- Expose existing task-update capabilities in the desktop interface with validation. Define whether a change applies immediately or on the next attempt in the interface instead of silently ignoring it.
- Add bulk selection and applicable actions, task details and recovery status, structured site-setting fields, and browser connection visibility. Keep record removal and file deletion clearly distinguished.
- Implement accessible modal naming, focus containment and restoration, and usable keyboard selection/actions. Choose large-list rendering optimizations based on measured behavior; a specific rendering library is not mandated.
- Preserve existing HLS scope. Clearly surface unsupported media combinations rather than expanding this milestone into a new streaming platform.
- Restore Windows installer packaging and GitHub Release preparation with version metadata, checksums, extension archives, prerequisites, upgrade/uninstall instructions, and unpacked extension installation guidance. Preserve queue/settings across supported upgrades. No paid certificates, accounts, or hosted services are required.
- Repository packaging and workflow preparation do not themselves execute a release or publish binaries. Actual publication is a distinct operational step.
- Prefer existing persistent models; add only the state required for pairing/revocation and durable handoff deduplication, with migration coverage. Exact schema and endpoint names remain implementation details.

## Testing Decisions

- The user confirmed the existing service/API boundary as the primary integration-test entry point. Prefer observable task outcomes, persisted state after restart, produced file bytes, and authorization responses over tests coupled to internal call order or component structure.
- Reuse deterministic local HTTP fixtures and existing engine, HLS, manager, service, API, storage, and desktop tests. Existing prior art includes hostile-server faults, validator changes, cancellation/resume, cross-host header stripping, HLS credential isolation, API authorization denial, queue scheduling, and persistence tests.
- Extend lower-level engine or HLS fixtures only where precise protocol or filesystem fault injection is necessary. Do not invent parallel production interfaces solely to make individual functions mockable.
- Add focused end-to-end acceptance for the boundaries that service tests cannot exercise: real Chrome/Edge handoff behavior, desktop keyboard interaction, and Windows installation/upgrade.

### Required acceptance scenarios

| Area | Observable acceptance |
| --- | --- |
| Credential boundaries | Same-origin authorized downloads work; cross-host, cross-port, and HTTPS-to-HTTP redirects do not receive sensitive headers. HLS child-resource requests enforce the same policy. |
| Partial-file safety | A preexisting symlink, hard link, or relevant Windows link/reparse-point case cannot cause another file's contents to be truncated. Check both ordinary single-stream download and HLS assembly. Platform-specific unavailable cases must be reported, not silently counted as passing. |
| Safe recovery | Interruption and service restart retain the task and safely resume supported resources. A changed validator or incompatible resource never silently merges old and new bytes. |
| Expired URL renewal | An expired signed URL can be replaced on the existing task. Compatible content resumes safely; incompatible or unprovable continuity produces explicit restart/recovery behavior. |
| Pairing | An unpaired or revoked extension cannot submit tasks or access protected service operations. Initial desktop approval permits the intended workflow. Ordinary web origins remain denied. |
| Website sessions | A protected test resource downloads after appropriate user-authorized session transfer. Permission denial and missing/expired credentials produce recoverable feedback. Cookies do not appear in logs or ordinary exports. |
| Durable handoff | Kill/restart the service after acknowledgement and confirm the accepted task remains. Simulated store failure yields no acceptance and leaves the browser download intact. |
| Retry ambiguity | Re-deliver the same handoff identity after a dropped response and verify one NimGet task. Preserve browser fallback when acknowledgement cannot be established and disclose possible duplicate browser activity. |
| Capture behavior | Fresh installation uses manual handoff. Opt-in capture obeys exclusions. Offline, rejected, unsupported, and cancellation-failure cases preserve access to the download and show the correct state. |
| Batch submission | In a batch containing valid and invalid entries, show each outcome. Retrying unresolved entries does not recreate the successful tasks. |
| Task management | Editing existing priority/connections/limits has documented observable effects. Bulk actions report individual failures and respect destructive-action choices. |
| Accessibility | Open, operate, and close dialogs using the keyboard; focus stays within the dialog and returns to its trigger. Task selection and relevant actions are exposed to assistive technology. |
| Queue usability | Exercise a representative large persisted queue while events update progress. Record queue size and observed responsiveness; do not declare an arbitrary performance threshold already agreed. |
| HLS limitations | Existing supported HLS fixtures remain valid. Unsupported media combinations are surfaced instead of presented as complete audiovisual downloads. |
| Installation | On Windows 11, install and launch without Go/Node.js. Verify documented runtime prerequisites, upgrade preservation, uninstall behavior, version metadata, and artifact checksums. The user explicitly waived Windows 10 runtime acceptance on 2026-11-16; compatible targeting is not evidence of a Windows 10 test run. |
| Browser acceptance | Exercise the released extension artifact on supported Chrome and Edge versions, recording the exact versions and manual-install steps. |

- Run the relevant Go regression tests, then the repository-wide test/vet/build checks and applicable race tests. Build the desktop frontend, validate generated bindings when interfaces change, and exercise the desktop module's relevant tests.
- Preserve existing cross-platform CI coverage and performance gates. Compare against the repository's existing benchmark evidence; this spec does not assert that NimGet outperforms IDM.
- Record checks that require unavailable operating systems, browsers, privileges, or external services. Release readiness requires the relevant acceptance evidence; compilation alone is insufficient.

## Out of Scope

- A NimGet account system, cloud synchronization, hosted backend, or remote internet-facing control service.
- Paid infrastructure, paid code-signing certificates, paid store accounts, and browser-store publication in this milestone.
- Automatic binary replacement or a new self-updating subsystem; provide a documented release-based update workflow.
- Firefox/Safari integration and a new Linux desktop product for this first milestone.
- DRM or payment-restriction bypass.
- DASH, live-stream recording, new separate-audio/subtitle acquisition, broad video-site extraction, a video overlay, and bundled ffmpeg. Existing supported HLS and optional conversion remain supported.
- Replacing Go/Wails/React, rewriting the established engine, or redesigning the entire visual identity.
- Broad new scheduling concepts such as named queues and calendar rules beyond preserving existing scheduling behavior.
- Guaranteed support for every website, browser-generated blob, nonreplayable request, or authenticated media provider.
- Publishing a release, acquiring external credentials, or asserting IDM performance parity merely by completing implementation work.

## Further Notes

- On 2026-11-16, the user explicitly directed implementation to skip Windows 10 runtime testing and use Windows 11 for acceptance. This supersedes the earlier two-version runtime acceptance requirement, including corresponding child-ticket criteria.
- Product scope and the testing seams above were confirmed in the conversation on 2026-11-16. Wire formats, exact schemas, packaging technology, performance targets, and a final implementation schedule were not separately approved and must not be portrayed as settled decisions.
- Suggested delivery order: safety and recovery fixes; browser authorization and handoff; desktop interaction improvements; packaging and complete acceptance. This is an implementation recommendation, not a report of completed work.
- The earlier source-only and browser-deferred policies are superseded for this planned milestone. Update conflicting project documentation with implementation so that shipped capabilities and limitations remain accurate.
- Initial code review identified credential-origin and partial-file safety risks, incomplete signed-URL renewal, batch retry duplication, and desktop usability gaps. Those findings still require regression reproduction and fixes; this spec is not a claim that vulnerabilities have been exploited.
- The earlier discovery run passed engine and HLS tests on the current Windows workspace. It did not complete full-suite, browser, installer, or cross-platform desktop acceptance. No implementation fixes are included in this specification task.
- Issue tracker: GitHub Issues for amemiya02/nimget. Triage label: `ready-for-agent`.
