import "./capture.js";
import { ensurePaired, pairedCredential, readCredential } from "./pairing.js";
import { deliverHandoffReceipt, manualHandoffPrefix, manualHandoffResult, sha256Hex, supportsHandoff, updateHandoffBadge } from "./handoff.js";
import {
  createWebsiteSessionDraft, readWebsiteSession, readWebsiteSessionDraft, readWebsiteSessionSnapshot,
  removeWebsiteSessionDraft, removeWebsiteSessionSnapshot, saveWebsiteSessionSnapshot, websiteSessionPrompt,
} from "./website-session.js";

const menuId = "nimget-send-link";
const websiteSessionMenuId = "nimget-send-with-website-session";
const inFlight = new Set();

// Connect as soon as possible; NimGet may not be running yet.
const connect = () => { void ensurePaired().catch(() => {}); };
chrome.runtime.onStartup.addListener(connect);

chrome.runtime.onInstalled.addListener(() => {
  connect();
  for (const [id, label] of [[menuId, "sendLink"], [websiteSessionMenuId, "sendWithWebsiteSession"]]) {
    chrome.contextMenus.remove(id, () => {
      void chrome.runtime.lastError; // No previous menu on fresh installation.
      chrome.contextMenus.create({
        id,
        title: chrome.i18n.getMessage(label),
        contexts: ["link"],
        documentUrlPatterns: ["http://*/*", "https://*/*"],
      });
    });
  }
});

chrome.contextMenus.onClicked.addListener((info, tab) => {
  if (info.menuItemId === menuId) void startHandoff(info.linkUrl).catch(showLocalFailure);
  if (info.menuItemId === websiteSessionMenuId) void openWebsiteSessionPrompt(info, tab).catch(showLocalFailure);
});

chrome.runtime.onMessage.addListener((message, sender, sendResponse) => {
  if (typeof message?.handoffId !== "string") return;
  if (message.type === "submitWebsiteSessionHandoff" &&
      sender.url === chrome.runtime.getURL(websiteSessionPrompt) + "#" + message.handoffId) {
    void (async () => {
      try {
        sendResponse(await submitWebsiteSessionHandoff(message.handoffId, message.useWebsiteSession === true));
      } catch {
        await showLocalFailure();
        sendResponse("handoffLocalFailure");
      }
    })();
    return true;
  }
  if (sender.url !== chrome.runtime.getURL("popup.html")) return;
  if (message.type !== "retryManualHandoff" && message.type !== "forgetManualHandoff") return;
  const action = message.type === "retryManualHandoff" ? retryHandoff : forgetHandoff;
  void (async () => {
    try {
      await action(message.handoffId);
    } catch {
      await showLocalFailure();
    } finally {
      sendResponse();
    }
  })();
  return true;
});

async function openWebsiteSessionPrompt(info, tab) {
  if (!supportsHandoff(info.linkUrl)) return showResult("handoffUnsupported");
  const credential = await readCredential() ?? await pairedCredential();
  if (!credential) return showResult("handoffPairingRequired");
  const handoffId = crypto.randomUUID();
  await createWebsiteSessionDraft(handoffId, info, tab?.id, await sha256Hex(credential));
  await chrome.tabs.create({ url: chrome.runtime.getURL(websiteSessionPrompt) + "#" + handoffId, windowId: tab?.windowId });
}

async function submitWebsiteSessionHandoff(handoffId, useWebsiteSession) {
  if (inFlight.has(handoffId)) return "handoffUnconfirmed";
  inFlight.add(handoffId);
  try {
    const draft = await readWebsiteSessionDraft(handoffId);
    if (!draft) return "websiteSessionDraftExpired";
    const credential = await readCredential();
    if (!credential || await sha256Hex(credential) !== draft.scope) return "handoffPairingChanged";
    let session;
    if (useWebsiteSession) {
      try {
        session = await readWebsiteSession(draft);
      } catch {
        return "websiteSessionUnavailable";
      }
      if (!session) return "websiteSessionUnavailable";
    }
    // Freeze the payload before contacting the desktop. A second click
    // cannot replace a session on an unresolved handoff.
    const record = await persistHandoff(handoffId, draft.url, credential, session, !useWebsiteSession);
    await removeWebsiteSessionDraft(handoffId);
    return await deliver(manualHandoffPrefix + handoffId, record, credential, session);
  } finally {
    inFlight.delete(handoffId);
  }
}

async function startHandoff(link) {
  if (!supportsHandoff(link)) {
    await showResult("handoffUnsupported");
    return;
  }
  const credential = await readCredential() ?? await pairedCredential();
  if (!credential) {
    await showResult("handoffPairingRequired");
    return;
  }
  const handoffId = crypto.randomUUID();
  inFlight.add(handoffId);
  try {
    const record = await persistHandoff(handoffId, link, credential);
    await deliver(manualHandoffPrefix + handoffId, record, credential);
  } finally {
    inFlight.delete(handoffId);
  }
}

async function persistHandoff(handoffId, link, credential, session, withoutWebsiteSession = false) {
  const record = {
    request: { handoffId, url: link },
    scope: await sha256Hex(credential),
    createdAt: Date.now(),
    requiresWebsiteSession: Boolean(session),
    withoutWebsiteSession,
  };
  // Persist only the identity and public request metadata across restarts.
  if (session) await saveWebsiteSessionSnapshot(handoffId, session);
  try {
    await chrome.storage.local.set({ [manualHandoffPrefix + handoffId]: record });
  } catch (error) {
    if (session) await removeWebsiteSessionSnapshot(handoffId);
    throw error;
  }
  return record;
}

async function retryHandoff(handoffId) {
  if (inFlight.has(handoffId)) return;
  inFlight.add(handoffId);
  try {
    const key = manualHandoffPrefix + handoffId;
    const { [key]: record } = await chrome.storage.local.get(key);
    if (!record) return;
    const credential = await readCredential();
    if (!credential || await sha256Hex(credential) !== record.scope) {
      await removeWebsiteSessionSnapshot(handoffId);
      await chrome.storage.local.set({ [key]: { ...record, feedback: "handoffPairingChanged" } });
      await showResult("handoffPairingChanged");
      return;
    }
    const session = record.requiresWebsiteSession ? await readWebsiteSessionSnapshot(handoffId) : undefined;
    await deliver(key, record, credential, session, true);
  } finally {
    inFlight.delete(handoffId);
  }
}

async function forgetHandoff(handoffId) {
  if (inFlight.has(handoffId)) return;
  await removeWebsiteSessionSnapshot(handoffId);
  await chrome.storage.local.remove(manualHandoffPrefix + handoffId);
  await showResult("handoffForgotten", "");
}

async function deliver(key, record, credential, session, retry = false) {
  let receipt;
  try {
    receipt = await deliverHandoffReceipt(record.request, credential, record.requiresWebsiteSession, session);
  } catch (error) {
    const rejected = {
      EXTENSION_CREDENTIAL_REQUIRED: "handoffPairingRequired",
      PAIRING_REQUIRED: "handoffPairingRequired",
      FORBIDDEN_ORIGIN: "handoffRejected",
      HANDOFF_CONFLICT: "handoffConflict",
      HANDOFF_NOT_FOUND: "websiteSessionRetryNeedsConsent",
      HANDOFF_REJECTED: "handoffRejected",
      INVALID_HANDOFF_ID: "handoffRejected",
      INVALID_REQUEST: "handoffRejected",
      UNSUPPORTED_LINK: "handoffUnsupported",
      INVALID_WEBSITE_SESSION: "websiteSessionUnavailable",
    }[error.code];
    if (rejected) {
      // A rejected retry can follow an earlier ambiguous acknowledgement. The
      // user retains the entry and can check the desktop queue or dismiss it.
      if (retry) {
        await chrome.storage.local.set({ [key]: { ...record, feedback: rejected } });
      } else {
        await removeWebsiteSessionSnapshot(record.request.handoffId);
        await chrome.storage.local.remove(key);
      }
      await showResult(rejected);
      return rejected;
    } else {
      const feedback = error.code === "HANDOFF_STORAGE_FAILED" ? "handoffStoreFailure" : "handoffUnconfirmed";
      await chrome.storage.local.set({ [key]: { ...record, feedback } });
      await showResult(feedback);
      return feedback;
    }
  }
  const memoryOnly = receipt.websiteSessionPersistence === "memory-only";
  const feedback = memoryOnly ? "handoffAcceptedMemoryOnly" :
    record.withoutWebsiteSession ? "handoffAcceptedWithoutWebsiteSession" : "handoffAccepted";
  await removeWebsiteSessionSnapshot(record.request.handoffId);
  await chrome.storage.local.remove(key);
  await showResult(feedback, memoryOnly ? "!" : "✓");
  return feedback;
}

async function showResult(result, badge = "!") {
  await chrome.storage.local.set({ [manualHandoffResult]: result });
  await updateHandoffBadge(result, badge);
}

function showLocalFailure() {
  // Storage itself can be unavailable. The toolbar still shows the result.
  return showResult("handoffLocalFailure").catch(() => updateHandoffBadge("handoffLocalFailure", "!").catch(() => {}));
}
