const serviceURL = new URL("http://127.0.0.1:51000/v1/extension/");
const storageKey = "pairingCredential";

export async function readCredential() {
  await chrome.storage.local.setAccessLevel({ accessLevel: "TRUSTED_CONTEXTS" });
  const stored = await chrome.storage.local.get(storageKey);
  return stored[storageKey];
}

export class ExtensionRequestError extends Error {
  constructor(code) {
    super(code);
    this.code = code;
  }
}

// Browser-only commands share the same profile credential. Reject redirects
// so neither the credential nor a command body can leave the loopback endpoint.
export async function requestExtension(command, body, token) {
  const credential = token ?? await readCredential();
  if (!credential) throw new ExtensionRequestError("EXTENSION_CREDENTIAL_REQUIRED");

  const url = new URL(command, serviceURL);
  if (url.origin !== serviceURL.origin || !url.pathname.startsWith(serviceURL.pathname)) {
    throw new TypeError("Invalid extension API path");
  }

  let response;
  try {
    response = await fetch(url, {
      method: "POST",
      headers: {
        Authorization: `Bearer ${credential}`,
        "Content-Type": "application/json",
      },
      body: JSON.stringify(body),
      credentials: "omit",
      cache: "no-store",
      redirect: "error",
      signal: AbortSignal.timeout(2500),
    });
  } catch {
    throw new ExtensionRequestError("DESKTOP_UNREACHABLE");
  }

  const result = await response.json().catch(() => null);
  if (!response.ok || !result) {
    throw new ExtensionRequestError(result?.error?.code ?? "REQUEST_FAILED");
  }
  return result;
}

export async function pairingStatus() {
  const token = await readCredential();
  return token ? requestExtension("pairing/status", {}, token) : { status: "disconnected" };
}

export async function requestPairing() {
  await chrome.storage.local.setAccessLevel({ accessLevel: "TRUSTED_CONTEXTS" });
  const token = Array.from(crypto.getRandomValues(new Uint8Array(32)), (byte) =>
    byte.toString(16).padStart(2, "0"),
  ).join("");
  // Keep the credential through popup closure or a lost HTTP response. Status
  // can recover the same request without a second approval.
  await chrome.storage.local.set({ [storageKey]: token });
  const browser = /\bEdg\//.test(navigator.userAgent) ? "edge" : "chrome";
  return requestExtension("pairing", { browser }, token);
}
