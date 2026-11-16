# Send a download link to NimGet

1. Open NimGet and [pair your Chrome or Edge profile](browser-pairing.md).
2. On a web page, right-click a public HTTP or HTTPS download link and choose **Download with NimGet**.
3. Check the NimGet extension icon. A check mark confirms that NimGet accepted the download task. An exclamation mark means the extension needs your attention. Open the extension popup for details. The task uses NimGet's standard destination and queue settings and appears in the desktop task list.

If the extension cannot confirm the handoff, open NimGet and choose **Retry** for that handoff in the popup. Retry uses the same identity, so it can recover an acknowledgement even after restarting NimGet or the browser. Each new click on **Download with NimGet** creates a separate handoff. If you prefer to download in Chrome or Edge after an unconfirmed handoff, check the NimGet queue first. Choose **Dismiss** to remove the retry from the popup. A dismissed handoff may have already created a task in NimGet.

The extension stores the selected link in its profile while a handoff is unresolved. Acknowledgement or dismissal removes the stored link. Retrying requires the same pairing that initiated the handoff. Browser-generated blob links and links with embedded credentials are unsupported. The context-menu command requests no website permissions and submits the link as a GET download without browser session headers. Links that require a website session can fail in the desktop even after successful task creation.

## Loopback API

Send `POST /v1/extension/handoffs` to `http://127.0.0.1:51000` using the [extension pairing authorization and origin rules](browser-pairing.md#loopback-api). The JSON body contains `handoffId`, a lowercase UUID created once per intent, and `url`, an absolute HTTP or HTTPS URL without embedded credentials.

| Response | JSON | Meaning |
| --- | --- | --- |
| HTTP 201 Created | `{"taskId":"<id>"}` | NimGet committed the task and its handoff identity together |
| HTTP 200 OK | `{"taskId":"<id>"}` | NimGet had already committed that handoff and is returning its original task ID |
| HTTP 400 Bad Request | `error.code` of `INVALID_HANDOFF_ID`, `UNSUPPORTED_LINK`, `INVALID_REQUEST`, or `HANDOFF_REJECTED` | The identity, link, or download request was rejected |
| HTTP 401 Unauthorized or 403 Forbidden | `error.code` including `EXTENSION_CREDENTIAL_REQUIRED` or `PAIRING_REQUIRED` | Origin or credential verification failed, or the pairing is no longer authorized |
| HTTP 409 Conflict | `error.code` of `HANDOFF_CONFLICT` | This pairing already committed the identity with a different payload |
| HTTP 500 Internal Server Error | `error.code` of `HANDOFF_STORAGE_FAILED` for task persistence failures | No acknowledgement was returned. Retrying with the original request can reconcile the outcome |

The SQLite migration preserves existing tasks and pairings. The receipt is scoped to the approved pairing and kept even if the user later removes the desktop task, so a delayed retry cannot recreate a deleted task. The receipt reflects the original commitment. It carries the task ID and remains valid as the task changes in the desktop.

Keep the payload stable when retrying. A digest binds the identity to the typed download request, including the URL as submitted. Additional request data that changes the task, such as future website session headers, belongs in the same request digest. For automatic capture, generate an identity once per browser download, validate the returned `taskId` before acting on it, and treat timeouts, missing responses, and errors as unconfirmed. Reconcile with the original identity. The API acknowledges task persistence independently of download completion or any actions taken by the browser downloader.
