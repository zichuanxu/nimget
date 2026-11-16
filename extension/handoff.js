import { ExtensionRequestError, requestExtension } from "./pairing.js";

export const manualHandoffPrefix = "manualHandoff:";
export const manualHandoffResult = "manualHandoffResult";

export async function submitHandoff(request, credential) {
  return (await submitHandoffWithReceipt(request, credential)).taskId;
}

export function submitHandoffWithReceipt(request, credential) {
  return requestReceipt("handoffs", request, credential, [200, 201]);
}

export function lookupHandoffReceipt(handoffId, credential) {
  return requestReceipt("handoffs/status", { handoffId }, credential, [200]);
}

// If the original session is gone, only reconcile an existing receipt.
export function deliverHandoffReceipt(request, credential, needsSession, session) {
  if (needsSession && !session) return lookupHandoffReceipt(request.handoffId, credential);
  return submitHandoffWithReceipt(session ? { ...request, websiteSession: session } : request, credential);
}

export async function sha256Hex(value) {
  const hash = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(value));
  return Array.from(new Uint8Array(hash), (byte) => byte.toString(16).padStart(2, "0")).join("");
}

export async function updateHandoffBadge(result, badge) {
  await chrome.action.setBadgeBackgroundColor({ color: badge === "✓" ? "#25723B" : "#A54025" });
  await chrome.action.setBadgeText({ text: badge });
  await chrome.action.setTitle({ title: chrome.i18n.getMessage(result) });
}

async function requestReceipt(path, request, credential, statuses) {
  const result = await requestExtension(path, request, credential, statuses);
  if (typeof result.taskId !== "string" || !result.taskId.trim()) {
    throw new ExtensionRequestError("HANDOFF_UNCONFIRMED");
  }
  return result;
}

export function supportsHandoff(link) {
  try {
    const url = new URL(link);
    return (url.protocol === "http:" || url.protocol === "https:") && !url.username && !url.password;
  } catch {
    return false;
  }
}
