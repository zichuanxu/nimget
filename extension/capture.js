import { captureHandoffPrefix, captureResultKey, readCaptureSettings } from "./capture-settings.js";
import { lookupHandoffReceipt, submitHandoffWithReceipt, supportsHandoff, updateHandoffBadge } from "./handoff.js";
import { ExtensionRequestError, readCredential } from "./pairing.js";
import { readWebsiteSession } from "./website-session.js";
import { wakeDesktop } from "./wake.js";

// A download that began as a form POST cannot be replayed by URL alone.
const requestWindowMs = 30_000;
const maxTrackedRequests = 256;
const requests = new Map();

function observeRequest({ url, method, timeStamp }) {
  requests.delete(url);
  if (requests.size >= maxTrackedRequests) requests.delete(requests.keys().next().value);
  requests.set(url, { method, timeStamp });
}

function postedRecently(...urls) {
  const now = Date.now();
  return urls.some((url) => {
    const request = requests.get(url);
    return request !== undefined && request.method !== "GET" && now - request.timeStamp < requestWindowMs;
  });
}

chrome.webRequest?.onBeforeRequest.addListener(observeRequest, {
  urls: ["http://*/*", "https://*/*"],
  types: ["main_frame", "sub_frame"],
});

// Earlier versions kept unresolved automatic handoffs for manual retry.
// Capture now either hands off or leaves the download with the browser.
void (async () => {
  const keys = await chrome.storage.local.getKeys();
  const stale = keys.filter((key) => key.startsWith(captureHandoffPrefix));
  if (stale.length > 0) await chrome.storage.local.remove(stale);
})().catch(() => {});

export function excluded(item, settings) {
  const links = [item.url, item.finalUrl, item.referrer].filter(supportsHandoff).map((link) => new URL(link));
  if (links.some(({ hostname }) => {
    const host = hostname.replace(/\.$/, "");
    return settings.sites.some((site) => host === site || host.endsWith("." + site));
  })) return true;
  const names = [item.filename, ...links.slice(0, 2).map(({ pathname }) => pathname)].filter(Boolean);
  return names.some((name) => {
    let candidate = name;
    try { candidate = decodeURIComponent(name); } catch { /* Use the browser's filename as presented. */ }
    return settings.types.some((type) => candidate.toLowerCase().endsWith("." + type));
  });
}

function eligible(item) {
  return !item.incognito && !item.byExtensionId && item.state !== "complete" &&
    supportsHandoff(item.url) && supportsHandoff(item.finalUrl || item.url) &&
    !postedRecently(item.url, item.finalUrl);
}

function sameOriginReferer(item) {
  try {
    const { origin } = new URL(item.url);
    return item.referrer && new URL(item.referrer).origin === origin ? origin + "/" : undefined;
  } catch {
    return undefined;
  }
}

// Holding suggest() keeps Chromium from showing a Save As dialog or writing
// the file while NimGet decides. Every path ends by releasing or canceling.
function downloadNamed(item, suggest) {
  let released = false;
  const release = () => {
    if (released) return;
    released = true;
    try { suggest(); } catch { /* The download is already gone. */ }
  };
  void considerDownload(item, release).catch(async () => {
    release();
    await showResult("captureLocalFailure").catch(() => {});
  });
  return true;
}

async function considerDownload(item, release) {
  const settings = await readCaptureSettings();
  if (!settings.enabled || !eligible(item) || excluded(item, settings)) return release();
  const credential = await readCredential();
  if (!credential) {
    release();
    return showResult("capturePairingRequired");
  }

  let websiteSession = null;
  try {
    websiteSession = await readWebsiteSession({ url: item.url, referer: sameOriginReferer(item) });
  } catch {
    /* Hand off without cookies; public downloads need none. */
  }
  const request = { handoffId: crypto.randomUUID(), url: item.url };
  const body = websiteSession ? { ...request, websiteSession } : request;
  let receipt = await handOff(body, credential);
  if (receipt === "captureDesktopUnavailable" && await wakeDesktop()) receipt = await handOff(body, credential);
  if (typeof receipt === "string") {
    // Give the download back first; the notice can follow.
    release();
    return showResult(receipt);
  }

  try {
    await chrome.downloads.cancel(item.id);
    await chrome.downloads.erase({ id: item.id });
  } catch {
    release();
    return showResult("captureCancelFailed");
  }
  release();
  const memoryOnly = receipt.websiteSessionPersistence === "memory-only";
  await showResult(memoryOnly ? "captureAcceptedMemoryOnly" : "captureAccepted", "✓");
}

// Returns the receipt, or the notice that explains why the browser keeps
// the download.
async function handOff(request, credential) {
  try {
    return await submitHandoffWithReceipt(request, credential);
  } catch (error) {
    if (error.code === "DESKTOP_UNREACHABLE") {
      // The request may have reached NimGet before the reply was lost.
      try {
        return await lookupHandoffReceipt(request.handoffId, credential);
      } catch (lookup) {
        return lookup.code === "DESKTOP_UNREACHABLE" ? "captureDesktopUnavailable" : "captureRejected";
      }
    }
    return {
      EXTENSION_CREDENTIAL_REQUIRED: "capturePairingRequired",
      PAIRING_REQUIRED: "capturePairingRequired",
      UNSUPPORTED_LINK: "captureUnsupported",
    }[error.code] ?? (error instanceof ExtensionRequestError ? "captureRejected" : "captureLocalFailure");
  }
}

if (chrome.downloads && !chrome.downloads.onDeterminingFilename.hasListener(downloadNamed)) {
  chrome.downloads.onDeterminingFilename.addListener(downloadNamed);
}

async function showResult(result, badge = "!") {
  await chrome.storage.local.set({ [captureResultKey]: result });
  await updateHandoffBadge(result, badge);
}
