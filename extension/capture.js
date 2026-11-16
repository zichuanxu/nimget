import {
  captureHandoffPrefix, capturePermissions, captureResultKey, captureSettingsKey,
  readCaptureSessionOrigins, readCaptureSettings,
} from "./capture-settings.js";
import { lookupHandoffReceipt, submitHandoffWithReceipt, supportsHandoff } from "./handoff.js";
import { readCredential } from "./pairing.js";
import {
  hasWebsiteSessionPermission, readWebsiteSession, readWebsiteSessionSnapshot,
  removeWebsiteSessionSnapshot, saveWebsiteSessionSnapshot,
} from "./website-session.js";

const requestWindowMs = 2500;
const maxTrackedRequests = 256;
const maxPendingHandoffs = 32;
const requests = new Map();
const inFlight = new Set();
let enabled = false;
let trackingFull = false;

function configureTracking(settings) {
  enabled = settings?.enabled === true;
  if (!enabled) {
    requests.clear();
    trackingFull = false;
  }
}

const settingsReady = (async () => {
  try {
    const settings = await readCaptureSettings();
    if (settings.enabled && await chrome.permissions.contains(capturePermissions)) configureTracking(settings);
  } catch {
    /* Leave capture off. */
  }
})();
chrome.storage.onChanged.addListener((changes, area) => {
  if (area === "local" && changes[captureSettingsKey]) configureTracking(changes[captureSettingsKey].newValue);
});

const navigations = { urls: capturePermissions.origins, types: ["main_frame", "sub_frame"] };

function observeWhenEnabled(observe) {
  return async (details) => {
    // A webRequest event can wake the worker before it has read the stored
    // opt-in. Preserve event order while that one read completes.
    await settingsReady;
    if (enabled) observe(details);
  };
}

function observeNavigation({ requestId, url, method, initiator, tabId, timeStamp }) {
  if (trackingFull || tabId < 0 || !supportsHandoff(url)) return;
  for (const [key, request] of requests) {
    if (request.attachmentAt && timeStamp - request.attachmentAt > requestWindowMs) requests.delete(key);
  }
  if (requests.size >= maxTrackedRequests) {
    // Evicting an in-flight request could make a concurrent navigation look
    // unique. Wait for the worker to restart or capture to be disabled.
    requests.clear();
    trackingFull = true;
    return;
  }
  const sameURL = [...requests.values()].filter((request) => request.url === url);
  for (const request of sameURL) request.ambiguous = true;
  requests.set(requestId, { url, method, initiator, tabId, ambiguous: sameURL.length > 0 });
}

function observeResponse({ requestId, url, method, responseHeaders, statusCode, timeStamp }) {
  const request = requests.get(requestId);
  if (!request || request.url !== url || request.method !== "GET" || method !== "GET" ||
      request.ambiguous || statusCode !== 200) return;
  const attachment = responseHeaders?.some(({ name, value }) =>
    name.toLowerCase() === "content-disposition" && /^\s*attachment(?:\s*;|\s*$)/i.test(value ?? ""));
  if (attachment) {
    request.attachmentAt = timeStamp;
    request.requiresContext = responseHeaders.some(({ name, value }) => {
      switch (name.toLowerCase()) {
        case "vary":
          return /(?:^|,)\s*(?:cookie|authorization|\*)\s*(?:,|$)/i.test(value ?? "");
        case "cache-control":
          return /(?:^|,)\s*private(?:\s*(?:=|,|$))/i.test(value ?? "");
        default:
          return false;
      }
    });
  } else {
    requests.delete(requestId);
  }
}

function forgetNavigation({ requestId }) {
  requests.delete(requestId);
}

function navigationEnded({ requestId }) {
  // Chromium can finish its navigation while it is still determining the
  // file name for the corresponding download.
  if (!requests.get(requestId)?.attachmentAt) requests.delete(requestId);
}

function claimNavigation(item) {
  if (item.incognito || item.byExtensionId || item.danger !== "safe" || !supportsHandoff(item.url) ||
      item.url !== item.finalUrl) return null;
  const started = Date.parse(item.startTime);
  if (!Number.isFinite(started)) return null;
  const candidates = [...requests].filter(([, request]) => request.url === item.url);
  if (candidates.length !== 1) return null;
  const [id, request] = candidates[0];
  if (request.ambiguous || !request.attachmentAt ||
      Math.abs(started - request.attachmentAt) > requestWindowMs) return null;
  requests.delete(id);
  return request;
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
  chrome.webRequest.onBeforeRequest.addListener(observeWhenEnabled(observeNavigation), navigations);
  chrome.webRequest.onHeadersReceived.addListener(observeWhenEnabled(observeResponse), navigations, ["responseHeaders"]);
  chrome.webRequest.onBeforeRedirect.addListener(observeWhenEnabled(forgetNavigation), navigations);
  chrome.webRequest.onCompleted.addListener(observeWhenEnabled(navigationEnded), navigations);
  chrome.webRequest.onErrorOccurred.addListener(observeWhenEnabled(navigationEnded), navigations);
  chrome.downloads.onCreated.addListener(downloadCreated);
  chrome.downloads.onDeterminingFilename.addListener(downloadNamed);
}

registerCaptureListeners();
chrome.permissions.onAdded.addListener(registerCaptureListeners);
chrome.permissions.onRemoved.addListener(({ permissions, origins }) => {
  if (permissions?.includes("downloads") || origins?.some((origin) => capturePermissions.origins.includes(origin))) {
    configureTracking();
  }
});

function excluded(item, initiator, settings) {
  const links = [item.url, item.finalUrl, item.referrer, initiator].filter(supportsHandoff).map((link) => new URL(link));
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
  void captureDownload(item, key).catch(showLocalFailure);
}

async function captureDownload(item, key) {
  inFlight.add(key);
  try {
    await settingsReady;
    const navigation = enabled ? claimNavigation(item) : null;
    const settings = await readCaptureSettings();
    if (!settings.enabled || !await chrome.permissions.contains(capturePermissions)) return;
    if (excluded(item, navigation?.initiator, settings)) return;
    if (!navigation) {
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
    const { hostname, origin } = new URL(item.url);
    const useSession = (await readCaptureSessionOrigins()).includes(origin);
    let websiteSession;
    if (navigation.requiresContext && !useSession) {
      await showResult("captureSessionUnavailable");
      return;
    }
    if (useSession) {
      // Cross-origin and opaque initiators may have a different cookie
      // context. Leave those downloads with Chromium.
      if (navigation.initiator === origin) {
        websiteSession = await readWebsiteSession({ url: item.url, referer: origin + "/", tabId: navigation.tabId });
      }
      if (!websiteSession) {
        await showResult("captureSessionUnavailable");
        return;
      }
    }
    const record = {
      request: { handoffId: crypto.randomUUID(), url: item.url },
      scope: await credentialDigest(credential),
      createdAt: Date.now(),
      host: hostname,
      ...(useSession && { useSession }),
    };
    // Persist before the request. If the worker stops or the reply is lost,
    // retry keeps the same identity and leaves the browser download alone.
    if (websiteSession) await saveWebsiteSessionSnapshot(record.request.handoffId, websiteSession);
    try {
      await chrome.storage.local.set({ [key]: record });
    } catch (err) {
      if (websiteSession) await removeWebsiteSessionSnapshot(record.request.handoffId);
      throw err;
    }
    await deliver(key, record, credential, item.id, websiteSession);
  } finally {
    inFlight.delete(key);
  }
}

async function credentialDigest(credential) {
  const hash = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(credential));
  return Array.from(new Uint8Array(hash), (byte) => byte.toString(16).padStart(2, "0")).join("");
}

async function deliver(key, record, credential, browserId, session) {
  let receipt;
  try {
    receipt = record.useSession && !session
      ? await lookupHandoffReceipt(record.request.handoffId, credential)
      : await submitHandoffWithReceipt(session ? { ...record.request, websiteSession: session } : record.request, credential);
  } catch (error) {
    const rejected = {
      EXTENSION_CREDENTIAL_REQUIRED: "capturePairingRequired",
      PAIRING_REQUIRED: "capturePairingRequired",
      HANDOFF_REJECTED: "captureRejected",
      INVALID_HANDOFF_ID: "captureRejected",
      INVALID_REQUEST: "captureRejected",
      FORBIDDEN_ORIGIN: "captureRejected",
      UNSUPPORTED_LINK: "captureUnsupported",
      INVALID_WEBSITE_SESSION: "captureSessionUnavailable",
      HANDOFF_NOT_FOUND: "captureSessionExpired",
      HANDOFF_CONFLICT: "captureRejected",
    }[error.code];
    if (rejected && browserId !== undefined) {
      await chrome.storage.local.remove(key);
      if (record.useSession) await removeWebsiteSessionSnapshot(record.request.handoffId);
      await showResult(rejected);
    } else {
      const feedback = rejected ?? (error.code === "HANDOFF_STORAGE_FAILED" ? "captureStoreFailure" : "captureUnconfirmed");
      await chrome.storage.local.set({ [key]: { ...record, feedback } });
      await showResult(feedback);
    }
    return;
  }

  if (record.useSession) await removeWebsiteSessionSnapshot(record.request.handoffId);
  const memoryOnly = receipt.websiteSessionPersistence === "memory-only";
  if (browserId === undefined) {
    await chrome.storage.local.remove(key);
    await showResult(memoryOnly ? "captureReconciledMemoryOnly" : "captureReconciled");
    return;
  }

  try {
    // Verify cancellation because Chrome can finish a small file before the
    // acknowledgement arrives, and cancel() can resolve for a finished item.
    await chrome.downloads.cancel(browserId);
    const [item] = await chrome.downloads.search({ id: browserId });
    if (item?.state !== "interrupted" || item.error !== "USER_CANCELED") throw new Error();
  } catch {
    const feedback = memoryOnly ? "captureCancelFailedMemoryOnly" : "captureCancelFailed";
    await chrome.storage.local.set({ [key]: { host: record.host, createdAt: record.createdAt, feedback } });
    await showResult(feedback);
    return;
  }
  await chrome.storage.local.remove(key);
  await showResult(memoryOnly ? "captureAcceptedMemoryOnly" : "captureAccepted", memoryOnly ? "!" : "✓");
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
    const { [key]: record } = await chrome.storage.local.get(key);
    if (!record) return;
    if (type === "dismissCaptureHandoff") {
      await chrome.storage.local.remove(key);
      if (record.useSession) await removeWebsiteSessionSnapshot(record.request.handoffId);
      await showResult("captureDismissed", "");
      return;
    }
    if (!record.request) return;
    const credential = await readCredential();
    if (!credential || await credentialDigest(credential) !== record.scope) {
      await showResult("capturePairingChanged");
      return;
    }
    const currentOrigins = record.useSession ? await readCaptureSessionOrigins() : [];
    const stillAuthorized = record.useSession && currentOrigins.includes(new URL(record.request.url).origin)
      && await hasWebsiteSessionPermission(record.request.url);
    // A lost reply can be looked up even when the browser session snapshot
    // is gone or the user has since removed that website from capture.
    const session = stillAuthorized ? await readWebsiteSessionSnapshot(record.request.handoffId) : undefined;
    await deliver(key, record, credential, undefined, session);
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
