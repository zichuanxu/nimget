import { readCredential } from "./pairing.js";
import { manualHandoffPrefix, manualHandoffResult, submitHandoff, supportsHandoff } from "./handoff.js";

const menuId = "nimget-send-link";
const inFlight = new Set();

chrome.runtime.onInstalled.addListener(() => {
  chrome.contextMenus.remove(menuId, () => {
    void chrome.runtime.lastError; // No previous menu on fresh installation.
    chrome.contextMenus.create({
      id: menuId,
      title: chrome.i18n.getMessage("sendLink"),
      contexts: ["link"],
      documentUrlPatterns: ["http://*/*", "https://*/*"],
    });
  });
});

chrome.contextMenus.onClicked.addListener((info) => {
  if (info.menuItemId === menuId) void startHandoff(info.linkUrl).catch(showLocalFailure);
});

chrome.runtime.onMessage.addListener((message, sender, sendResponse) => {
  if (sender.url !== chrome.runtime.getURL("popup.html")) return;
  if (message?.type !== "retryManualHandoff" && message?.type !== "forgetManualHandoff") return;
  if (typeof message.handoffId !== "string") return;
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

async function credentialDigest(credential) {
  const hash = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(credential));
  return Array.from(new Uint8Array(hash), (byte) => byte.toString(16).padStart(2, "0")).join("");
}

async function startHandoff(link) {
  if (!supportsHandoff(link)) {
    await showResult("handoffUnsupported");
    return;
  }
  const credential = await readCredential();
  if (!credential) {
    await showResult("handoffPairingRequired");
    return;
  }
  const handoffId = crypto.randomUUID();
  const key = manualHandoffPrefix + handoffId;
  const record = {
    request: { handoffId, url: link },
    scope: await credentialDigest(credential),
    createdAt: Date.now(),
  };
  inFlight.add(handoffId);
  try {
    // Persist the identity and request before contacting the desktop. A retry
    // after a lost response or browser restart sends this same request.
    await chrome.storage.local.set({ [key]: record });
    await deliver(key, record, credential);
  } finally {
    inFlight.delete(handoffId);
  }
}

async function retryHandoff(handoffId) {
  if (inFlight.has(handoffId)) return;
  inFlight.add(handoffId);
  try {
    const key = manualHandoffPrefix + handoffId;
    const { [key]: record } = await chrome.storage.local.get(key);
    if (!record) return;
    const credential = await readCredential();
    if (!credential || await credentialDigest(credential) !== record.scope) {
      await chrome.storage.local.set({ [key]: { ...record, feedback: "handoffPairingChanged" } });
      await showResult("handoffPairingChanged");
      return;
    }
    await deliver(key, record, credential, true);
  } finally {
    inFlight.delete(handoffId);
  }
}

async function forgetHandoff(handoffId) {
  if (inFlight.has(handoffId)) return;
  await chrome.storage.local.remove(manualHandoffPrefix + handoffId);
  await showResult("handoffForgotten", "");
}

async function deliver(key, record, credential, retry = false) {
  try {
    await submitHandoff(record.request, credential);
  } catch (error) {
    const rejected = {
      EXTENSION_CREDENTIAL_REQUIRED: "handoffPairingRequired",
      PAIRING_REQUIRED: "handoffPairingRequired",
      FORBIDDEN_ORIGIN: "handoffRejected",
      HANDOFF_CONFLICT: "handoffConflict",
      HANDOFF_REJECTED: "handoffRejected",
      INVALID_HANDOFF_ID: "handoffRejected",
      INVALID_REQUEST: "handoffRejected",
      UNSUPPORTED_LINK: "handoffUnsupported",
    }[error.code];
    if (rejected) {
      // A rejected retry can follow an earlier ambiguous acknowledgement. The
      // user retains the entry and can check the desktop queue or dismiss it.
      if (retry) {
        await chrome.storage.local.set({ [key]: { ...record, feedback: rejected } });
      } else {
        await chrome.storage.local.remove(key);
      }
      await showResult(rejected);
    } else {
      const feedback = error.code === "HANDOFF_STORAGE_FAILED" ? "handoffStoreFailure" : "handoffUnconfirmed";
      await chrome.storage.local.set({ [key]: { ...record, feedback } });
      await showResult(feedback);
    }
    return;
  }
  await showResult("handoffAccepted", "✓");
  await chrome.storage.local.remove(key);
}

async function showResult(result, badge = "!") {
  await chrome.storage.local.set({ [manualHandoffResult]: result });
  await updateBadge(result, badge);
}

async function updateBadge(result, badge) {
  await chrome.action.setBadgeBackgroundColor({ color: badge === "✓" ? "#25723B" : "#A54025" });
  await chrome.action.setBadgeText({ text: badge });
  await chrome.action.setTitle({ title: chrome.i18n.getMessage(result) });
}

function showLocalFailure() {
  // Storage itself can be unavailable. The toolbar still shows the result.
  return showResult("handoffLocalFailure").catch(() => updateBadge("handoffLocalFailure", "!").catch(() => {}));
}
