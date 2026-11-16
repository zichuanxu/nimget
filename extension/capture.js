import { captureHandoffPrefix, capturePermissions, captureResultKey, captureSettingsKey, readCaptureSettings } from "./capture-settings.js";
import { submitHandoff, supportsHandoff } from "./handoff.js";
import { readCredential } from "./pairing.js";

const requestWindowMs = 2500;
const maxTrackedRequests = 256;
const maxPendingHandoffs = 32;
const requests = new Map();
const inFlight = new Set();
let enabled = false;

function configureTracking(settings) {
  enabled = settings?.enabled === true;
  if (!enabled) requests.clear();
}

void readCaptureSettings().then(configureTracking).catch(() => {});
chrome.storage.onChanged.addListener((changes, area) => {
  if (area === "local" && changes[captureSettingsKey]) configureTracking(changes[captureSettingsKey].newValue);
});

const navigations = { urls: capturePermissions.origins, types: ["main_frame", "sub_frame"] };

function observeNavigation({ requestId, url, method, tabId, timeStamp }) {
  if (!enabled || tabId < 0 || !supportsHandoff(url)) return;
  for (const [key, request] of requests) {
    if (timeStamp - request.started > requestWindowMs) requests.delete(key);
  }
  if (requests.size >= maxTrackedRequests) requests.delete(requests.keys().next().value);
  const sameURL = [...requests.values()].filter((request) => request.url === url);
  for (const request of sameURL) request.ambiguous = true;
  requests.set(requestId, { url, method, started: timeStamp, ambiguous: sameURL.length > 0 });
}

function observeResponse({ requestId, url, method, responseHeaders, statusCode, timeStamp }) {
  const request = requests.get(requestId);
  if (!request || request.url !== url || request.method !== "GET" || method !== "GET" ||
      request.ambiguous || statusCode !== 200) return;
  const attachment = responseHeaders?.some(({ name, value }) =>
    name.toLowerCase() === "content-disposition" && /^\s*attachment(?:\s*;|\s*$)/i.test(value ?? ""));
  if (attachment) request.attachmentAt = timeStamp;
}

function forgetNavigation({ requestId }) {
  requests.delete(requestId);
}

function claimNavigation(item) {
  if (item.incognito || item.byExtensionId || item.danger !== "safe" || !supportsHandoff(item.url) ||
      item.url !== item.finalUrl) return false;
  const started = Date.parse(item.startTime);
  const candidates = [...requests].filter(([, request]) => request.url === item.url);
  if (candidates.length !== 1) return false;
  const [id, request] = candidates[0];
  if (request.ambiguous || !request.attachmentAt ||
      Math.abs(started - request.attachmentAt) > requestWindowMs) return false;
  requests.delete(id);
  return true;
}

function downloadCreated(item) {
  // Filename determination can follow onCreated. Wait for it if Chrome has
  // yet to supply a name, so server-provided extensions can be excluded.
  if (item.filename) considerDownload(item);
}

function downloadNamed(item, suggest) {
  suggest();
  considerDownload(item);
}

function registerCaptureListeners() {
  if (!chrome.downloads || !chrome.webRequest || chrome.downloads.onCreated.hasListener(downloadCreated)) return;
  chrome.webRequest.onBeforeRequest.addListener(observeNavigation, navigations);
  chrome.webRequest.onHeadersReceived.addListener(observeResponse, navigations, ["responseHeaders"]);
  chrome.webRequest.onBeforeRedirect.addListener(forgetNavigation, navigations);
  chrome.webRequest.onErrorOccurred.addListener(forgetNavigation, navigations);
  chrome.downloads.onCreated.addListener(downloadCreated);
  chrome.downloads.onDeterminingFilename.addListener(downloadNamed);
}

registerCaptureListeners();
chrome.permissions.onAdded.addListener(registerCaptureListeners);
chrome.permissions.onRemoved.addListener(() => {
  requests.clear();
});

function excluded(item, settings) {
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

function considerDownload(item) {
  if (item.incognito) return;
  const key = captureHandoffPrefix + item.id + ":" + item.startTime;
  if (inFlight.has(key)) return;
  const verified = enabled && claimNavigation(item);
  void captureDownload(item, key, verified).catch(showLocalFailure);
}

async function captureDownload(item, key, verified) {
  inFlight.add(key);
  try {
    const settings = await readCaptureSettings();
    if (!settings.enabled) return;
    if (excluded(item, settings)) return;
    if (!verified) {
      await showResult("captureUnsupported");
      return;
    }
    const credential = await readCredential();
    if (!credential) {
      await showResult("capturePairingRequired");
      return;
    }
    const keys = await chrome.storage.local.getKeys();
    if (keys.includes(key)) return;
    if (keys.filter((entry) => entry.startsWith(captureHandoffPrefix)).length >= maxPendingHandoffs) {
      await showResult("capturePendingLimit");
      return;
    }
    const record = {
      request: { handoffId: crypto.randomUUID(), url: item.url },
      scope: await credentialDigest(credential),
      createdAt: Date.now(),
      host: new URL(item.url).hostname,
    };
    // Persist before the request. If the worker stops or the reply is lost,
    // retry keeps the same identity and leaves the browser download alone.
    await chrome.storage.local.set({ [key]: record });
    await deliver(key, record, credential, item.id);
  } finally {
    inFlight.delete(key);
  }
}

async function credentialDigest(credential) {
  const hash = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(credential));
  return Array.from(new Uint8Array(hash), (byte) => byte.toString(16).padStart(2, "0")).join("");
}

async function deliver(key, record, credential, browserId) {
  try {
    await submitHandoff(record.request, credential);
  } catch (error) {
    const rejected = {
      EXTENSION_CREDENTIAL_REQUIRED: "capturePairingRequired",
      PAIRING_REQUIRED: "capturePairingRequired",
      HANDOFF_REJECTED: "captureRejected",
      INVALID_HANDOFF_ID: "captureRejected",
      INVALID_REQUEST: "captureRejected",
      FORBIDDEN_ORIGIN: "captureRejected",
      UNSUPPORTED_LINK: "captureUnsupported",
    }[error.code];
    if (rejected && browserId !== undefined) {
      await chrome.storage.local.remove(key);
      await showResult(rejected);
    } else {
      const feedback = rejected ?? (error.code === "HANDOFF_STORAGE_FAILED" ? "captureStoreFailure" : "captureUnconfirmed");
      await chrome.storage.local.set({ [key]: { ...record, feedback } });
      await showResult(feedback);
    }
    return;
  }

  if (browserId === undefined) {
    await chrome.storage.local.remove(key);
    await showResult("captureReconciled");
    return;
  }

  try {
    // Verify cancellation because Chrome can finish a small file before the
    // acknowledgement arrives, and cancel() can resolve for a finished item.
    await chrome.downloads.cancel(browserId);
    const [item] = await chrome.downloads.search({ id: browserId });
    if (item?.state !== "interrupted" || item.error !== "USER_CANCELED") throw new Error();
  } catch {
    await chrome.storage.local.set({ [key]: { host: record.host, createdAt: record.createdAt, feedback: "captureCancelFailed" } });
    await showResult("captureCancelFailed");
    return;
  }
  await chrome.storage.local.remove(key);
  await showResult("captureAccepted", "✓");
}

chrome.runtime.onMessage.addListener((message, sender, sendResponse) => {
  if (sender.url !== chrome.runtime.getURL("popup.html")) return;
  if (!["retryCaptureHandoff", "dismissCaptureHandoff"].includes(message?.type)) return;
  const { key } = message;
  if (typeof key !== "string" || !key.startsWith(captureHandoffPrefix)) return;
  void (async () => {
    try {
      await updatePendingHandoff(key, message.type);
    } catch {
      await showLocalFailure();
    } finally {
      sendResponse();
    }
  })();
  return true;
});

async function updatePendingHandoff(key, type) {
  if (inFlight.has(key)) return;
  inFlight.add(key);
  try {
    if (type === "dismissCaptureHandoff") {
      await chrome.storage.local.remove(key);
      await showResult("captureDismissed", "");
      return;
    }
    const { [key]: record } = await chrome.storage.local.get(key);
    if (!record?.request) return;
    const credential = await readCredential();
    if (!credential || await credentialDigest(credential) !== record.scope) {
      await showResult("capturePairingChanged");
      return;
    }
    await deliver(key, record, credential);
  } finally {
    inFlight.delete(key);
  }
}

async function showResult(result, badge = "!") {
  await chrome.storage.local.set({ [captureResultKey]: result });
  await updateBadge(result, badge);
}

async function updateBadge(result, badge) {
  await chrome.action.setBadgeBackgroundColor({ color: badge === "✓" ? "#25723B" : "#A54025" });
  await chrome.action.setBadgeText({ text: badge });
  await chrome.action.setTitle({ title: chrome.i18n.getMessage(result) });
}

function showLocalFailure() {
  return showResult("captureLocalFailure").catch(() => updateBadge("captureLocalFailure", "!").catch(() => {}));
}
