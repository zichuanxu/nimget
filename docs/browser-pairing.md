# Pair Chrome or Edge with NimGet

1. Download and verify the [Windows installer and Chromium extension archive](windows-release.md) from the same release. Extract the ZIP to a directory you can keep for later updates. For a source build, use the `extension` directory in the checkout.
2. Start the NimGet desktop app. If the CLI service is running, stop it first so the desktop can open the loopback connection.
3. Open `chrome://extensions` in Chrome or `edge://extensions` in Edge. Turn on **Developer mode**, select **Load unpacked**, and choose the extracted directory containing `manifest.json`.
4. The extension connects to NimGet by itself when it is installed, when the browser starts, when you open its popup, and on the first download it takes over. Open **NimGet** from the browser's Extensions menu to check that it shows **Connected**.

Repeat the steps for each browser profile you want to connect. Each connection appears in NimGet under **Settings → Browser extensions**.

### Automatic connection

**Connect the NimGet extension automatically** in **Settings → Browser extensions** is on by default. NimGet then approves pairing requests from the NimGet extension without an approval code. The loopback API accepts these requests only from the NimGet extension ID, so web pages and other extensions can't pair. The approval code doesn't protect against programs running on your computer, because they can already use NimGet's administrator token.

Turn the setting off and save to approve each browser yourself:

1. Open **NimGet** from the browser's Extensions menu. Select **Request pairing** and keep the request ID and approval code available.
2. In NimGet, open **Settings → Browser extensions**. Find the matching request ID, enter the code, and select **Approve**. The extension displays **Connected**. Select **Deny** to decline an unfamiliar request.

A request that is still waiting is approved the next time the extension asks after you turn the setting back on.

### Revoke and delete connections

Select **Revoke access** to end a connection. A revoked or denied browser stays disconnected, even with automatic connection on, until you select **Request pairing** in its extension. Select **Delete** on a revoked or denied record to remove it from the list. NimGet also forgets that pairing's handoff receipts. If the extension is still installed, it then reconnects as a new browser.

The browser shows **Disconnected** before pairing or while it cannot reach NimGet, **Awaiting approval** during a pending request, **Connected** after approval, and **Pairing denied** or **Access revoked** after the corresponding desktop action. A **Connected** desktop record means the profile remains authorized. The extension verifies desktop reachability when its popup is open. Closing the desktop window to the tray keeps the service running. Quitting the desktop stops it. Pairing does not require an account.

The extension asks for access to downloads, cookies, and all HTTP and HTTPS sites when you install it. It uses them to take over browser downloads with their website session and to reach the local service at `http://127.0.0.1:51000`. Pairing uses extension storage for its credential. See [browser handoff](browser-handoff.md) for both options.

## Update the extension

1. Download and verify the archive for the same version as the upgraded desktop app.
2. Quit each browser using the extension directory. Extract the new archive to a temporary directory and replace the contents of the existing NimGet extension directory with the new files.
3. Reopen the browser and select **Reload** on the NimGet extension card. Repeat in each browser profile using the directory. Open the NimGet popup and verify that it shows **Connected**.

Updating the files in place keeps the installed extension and its local browser storage. If you remove the extension or clear its storage, revoke and delete the old pairing in the desktop app. The reinstalled extension connects again by itself.

## Loopback API

Both requests use `POST`, `Content-Type: application/json` and `Authorization: Bearer <credential>` on `http://127.0.0.1:51000`.

| Path | JSON body | Behavior |
| --- | --- | --- |
| `/v1/extension/pairing` | `{"browser":"chrome"}` or `{"browser":"edge"}` | Creates a pending request or returns the existing record for that credential. With automatic connection on, a pending request is approved and the response reports `connected` without a code |
| `/v1/extension/pairing/status` | `{}` | Reports the current status for that credential without changing it |

An HTTP 200 response contains `status` (`disconnected`, `awaiting-approval`, `connected`, `denied`, or `revoked`), `id` when a record exists, and its eight-character `code` while awaiting approval. The popup and desktop show the last six characters of the record ID so the user can match a request. A pending request expires after ten minutes. At most 16 requests may await approval at a time. An invalid credential returns 401, an untrusted origin or host returns 403, and a full pending list returns 429.

The listen address must be loopback and the shared Host guard accepts only `127.0.0.1`, `localhost`, or `::1`. The browser routes accept the exact `Origin: chrome-extension://cbpkcafcmablmlkpkmmeincekjglfafb` fixed by the unpacked manifest's public key. This applies to preflights as well. Ordinary web pages and extensions with other IDs cannot call these routes. The CLI routes reject browser origins entirely and keep their separate administrator token. The origin identifies the browser extension in the browser's security model. It does not authenticate a local program that can open a socket and supply its own HTTP headers.

Desktop pairing commands use in-process Wails bindings. A browser command under `/v1/extension/` that needs a pairing must call the shared `extensionPairingStatus` helper on each request and require `pairing.Connected`. The returned `id` identifies the authorized pairing for scoping handoff operations. Extension code can send a command through `requestExtension` in `extension/pairing.js`, which binds the request to the loopback API and rejects redirects.

## Credential lifecycle

The extension generates 32 random bytes for each new request, encoded as 64 lowercase hexadecimal characters. It stores the credential in that browser profile's extension-local storage before sending the request, so an interrupted request can be checked with the same credential. Browser storage is limited to trusted extension contexts.

The desktop stores a SHA-256 verifier of the random credential in the existing SQLite database. The desktop list and logs omit the credential and the confirmation code. The confirmation value exists separately during the pending period and is cleared when the desktop responds. Desktop approval, denial, and revocation are persisted before the UI reports success. A pending request may be approved after restarting NimGet if it has not expired. Approved and revoked states survive desktop and browser restarts. Requests using a denied or revoked credential retain that state. Reinstalling the extension or clearing its local storage requires a new pairing. Revoke the old record from the desktop to invalidate the previous credential.

## Acceptance steps

Use separate browser profiles and a clean NimGet data directory and record the Windows, Chrome, and Edge versions tested. In each browser, confirm that the unpacked extension appears with the expected ID and follow the pairing steps above. In the desktop, deny one request and confirm the popup displays **Pairing denied**. Request again and try an incorrect code before approving with the code from the popup. Quit and restart NimGet, then restart the browser using the same profile, and confirm the connection returns. Revoke the pairing, restart NimGet again, and verify it stays revoked. Request a new pairing and confirm it awaits approval. See [Chrome's unpacked extension instructions](https://developer.chrome.com/docs/extensions/get-started/tutorial/hello-world#load-unpacked) and [Edge's sideloading instructions](https://learn.microsoft.com/en-us/microsoft-edge/extensions/getting-started/extension-sideloading).
