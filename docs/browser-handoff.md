# Send a download link to NimGet

1. Open NimGet and [pair your Chrome or Edge profile](browser-pairing.md).
2. On a web page, right-click an HTTP or HTTPS download link and choose **NimGet → Download with NimGet**. For a link that needs your signed-in session, choose **NimGet → Download with NimGet using website session…**.
3. Check the NimGet extension icon. A check mark confirms that NimGet accepted the download task. An exclamation mark means the extension needs your attention. Open the extension popup for details. The task uses NimGet's standard destination and queue settings and appears in the desktop task list.

If the extension cannot confirm the handoff, open NimGet and choose **Retry** for that handoff in the popup. Retry uses the same identity, so it can recover an acknowledgement even after restarting NimGet or the browser. Each new click on **Download with NimGet** creates a separate handoff. If you prefer to download in Chrome or Edge after an unconfirmed handoff, check the NimGet queue first. Choose **Dismiss** to remove the retry from the popup. A dismissed handoff may have already created a task in NimGet.

The extension stores the selected link in its profile while a handoff is unresolved. Acknowledgement or dismissal removes the stored link. Retrying requires the same pairing that initiated the handoff. Browser-generated blob links and links with embedded credentials are unsupported. **Download with NimGet** requests no website permissions and sends a GET download without browser session headers.

## Downloads that require a website session

Sign in to the site in the paired browser profile. Use the website session command on the link. NimGet opens a tab explaining the requested access. **Use website session** asks Chrome or Edge for cookie access to the target hostname. Browser cookie permissions apply across ports on that hostname. HTTPS downloads ask for both schemes so the browser can supply Secure and non-Secure cookies. If you decline, select **Send without session** to send a public link, or continue in the browser. Browser permission grants can remain in your extension's site access settings. For each manual handoff that needs cookies, choose **Use website session**.

NimGet uses the originating tab's browser cookie store. It asks the browser only for cookies matching the target link and excludes partitioned cookies. Secure cookies travel only on HTTPS links. Strict SameSite cookies require the page and any clicked frame to have the same origin as the target. For that same-origin case, NimGet also sends the origin as a referrer. The browser user agent accompanies the session. The cookies do not follow a redirect or media request to a different scheme, hostname, or port. Sites that require a custom authentication header, a full referrer, or an unsupported browser request can still be downloaded in the browser.

Website cookies live only in browser extension memory while a handoff is unresolved. A confirmed or dismissed handoff clears that copy. If the browser restarts or the cookie snapshot has expired, **Retry** checks the original receipt without sending new cookies. If there is no receipt, check the desktop queue before sending the link again with a fresh session.

The desktop encrypts website sessions using its existing OS-backed key. The expiry for starting a download attempt is the earliest of the transferred cookies' known expirations and 24 hours after collection. The desktop removes the stored session when the task completes, is deleted, or has its source or headers replaced. If the keyring is unavailable, NimGet can use the cookies now but shows a warning that authorization may need to be renewed after a NimGet restart. Website sessions are omitted from logs and queue exports.

If the site rejects the session or NimGet cannot recover it, sign in again and send the link using a fresh website session, or use **Refresh download link** on the existing desktop task to supply fresh request context. Acknowledgement means NimGet saved the task. Check the desktop task for download completion.

## Automatic capture

Open the NimGet extension popup and expand **Automatic capture**. Check **Send eligible downloads to NimGet**, save, and allow the optional browser permissions. Automatic capture starts off in each profile. Clear the checkbox and save to stop it and release its browser download and broad website permissions.

Enter excluded hostnames to keep downloads to or from those sites in the browser. Each entry includes its subdomains. For file-type exclusions, enter extensions such as `.iso` and `.tar.gz`. The extension checks the URL filename and the name provided by the browser. Settings are stored in the current browser profile.

NimGet hands off eligible direct HTTP(S) attachments when the extension observes a single matching GET navigation. Chromium's download events omit the HTTP method and web request ID, so the extension correlates downloads with nearby navigation events by URL and time. Observed competing requests to the same URL, redirects, POSTs, browser-generated files, and downloads without enough request information stay in the browser. The extension keeps bounded navigation metadata in memory, including recent requests that could make a match ambiguous. Ordinary navigation history, request bodies, and request header values are not saved.

Public capture needs no cookie permission. To share a website session automatically, add the complete origin under **Websites allowed to share sessions** and save that list separately. A different scheme, subdomain, or port needs its own entry. NimGet only attempts to use that session for a same-origin download when access is already granted. If the session is unavailable, or the server marks the response as private or varying by credentials and the site lacks your consent, the browser keeps the download. Removing an origin from the list stops automatic session access for that origin. For other protected downloads, use **Download with NimGet using website session…** from the browser menu.

The browser download is canceled only after NimGet returns a durable task receipt. A rejection, timeout, unconfirmed reply, or unavailable desktop leaves the browser downloading. Check the popup for a retry or an explanation. **Retry in NimGet** reuses the original identity and leaves the browser download alone. Check NimGet's queue and the browser downloads list before retrying, since both apps may already be downloading the file. A cancellation failure leaves the accepted NimGet task and a warning in the extension popup.

### Automatic capture acceptance

On Windows 11 25H2 (build 26200.9974), Google Chrome 157.0.8103.51 and Microsoft Edge 157.0.4341.60 ran the unpacked extension with isolated browser profiles and NimGet databases. Both browsers completed a download on a fresh installation, displayed the opt-in permission prompt, and kept downloads excluded by hostname or by the server-provided filename. POST, redirected, and private responses without session consent stayed in the browser. Public handoffs omitted cookies, and the allowed same-origin fixture downloaded the expected protected bytes. The saved setting and exclusions survived browser restart. The test harness reinstalled the unpacked extension after restart, so optional browser grants had to be restored. Both browsers displayed the paused state until renewed access.

The browser left a download active while the acknowledgement was held and canceled it after the receipt arrived. A loopback acceptance fixture in front of the Go service injected a dropped connection, rejection, storage-error response, malformed acknowledgement, timeout, and a response lost after durable acceptance. These cases kept the browser download. Retry after restarting the service reused the same identity and verified the original task and file. An injected browser cancellation error kept the accepted task, let the browser finish, and displayed the duplicate-download warning.

Run the service and extension receipt regressions with `go test ./internal/service ./internal/api` and `node --test tests/extension/*.test.mjs`.

## Loopback API

Send `POST /v1/extension/handoffs` to `http://127.0.0.1:51000` using the [extension pairing authorization and origin rules](browser-pairing.md#loopback-api). The JSON body contains `handoffId`, a lowercase UUID created once per intent, and `url`, an absolute HTTP or HTTPS URL without embedded credentials. A protected handoff adds `websiteSession` with a `cookie` header value and `expiresAt` as Unix seconds. Optional `referer` must be the target origin with a trailing slash and `userAgent` is the browser's user agent.

| Response | JSON | Meaning |
| --- | --- | --- |
| HTTP 201 Created | `{"taskId":"<id>"}` | NimGet committed the task and its handoff identity together |
| HTTP 200 OK | `{"taskId":"<id>"}` | NimGet had already committed that handoff and is returning its original task ID |
| HTTP 400 Bad Request | `error.code` of `INVALID_HANDOFF_ID`, `UNSUPPORTED_LINK`, `INVALID_REQUEST`, `INVALID_WEBSITE_SESSION`, or `HANDOFF_REJECTED` | The identity, link, website session, or download request was rejected |
| HTTP 401 Unauthorized or 403 Forbidden | `error.code` including `EXTENSION_CREDENTIAL_REQUIRED` or `PAIRING_REQUIRED` | Origin or credential verification failed, or the pairing is no longer authorized |
| HTTP 409 Conflict | `error.code` of `HANDOFF_CONFLICT` | This pairing already committed the identity with a different payload |
| HTTP 500 Internal Server Error | `error.code` of `HANDOFF_STORAGE_FAILED` for task persistence failures | No acknowledgement was returned. Retrying with the original request can reconcile the outcome |

Protected receipts also contain `websiteSessionPersistence` with `secure` or `memory-only`. Show the restart limitation for `memory-only`. For an unresolved request whose cookie snapshot is unavailable, send `POST /v1/extension/handoffs/status` with only `handoffId` and the same pairing authorization. It returns the committed receipt or HTTP 404 with `HANDOFF_NOT_FOUND` if no receipt is visible yet.

The SQLite migration preserves existing tasks, pairings, and receipts. Receipts persist after task deletion so delayed retries cannot recreate a deleted task. Keep the payload stable when retrying. The digest binds the identity to the typed request, including the URL as submitted. Protected requests include the website session in a digest keyed by the pairing credential.

For automatic capture, generate an identity once per browser download, validate the returned `taskId` before acting on it, and treat timeouts, missing responses, and errors as unconfirmed. Reconcile with the original identity. Extension code may call `hasWebsiteSessionPermission(url)` from `website-session.js` to check existing access without prompting. Permission alone does not authorize collecting a new snapshot. The website-session command asks the user to approve use on each download. The API acknowledges task persistence independently of download completion or any actions taken by the browser downloader.

## Website session acceptance

On Windows 11 25H2 (build 26200.9974), the unpacked extension was exercised in Google Chrome 157.0.8103.51 and Microsoft Edge 157.0.4341.60 with fresh profiles and pairing approved in the NimGet desktop app. In each browser, declining the browser's permission prompt allowed the public fallback, granting access downloaded the exact protected fixture bytes, and an expired cookie produced a failed task with recovery guidance. The protected HTTP fixture required a matching HttpOnly cookie, browser user agent, and same-origin referrer, and checked that a cookie from another path was omitted.

Local service tests cover encrypted storage, healthy and unavailable keyring restart scenarios, receipt lookup after restart, changed-session conflicts, a cross-port redirect, and ordinary export and diagnostics redaction. Run them with `go test ./internal/service -run TestWebsiteSessionHandoff`. Run the extension permission and cookie selection tests with `node --test tests/extension/website-session.test.mjs`.
