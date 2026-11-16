import { ExtensionRequestError, requestExtension } from "./pairing.js";

export const manualHandoffPrefix = "manualHandoff:";
export const manualHandoffResult = "manualHandoffResult";

export async function submitHandoff(request, credential) {
  return (await submitHandoffWithReceipt(request, credential)).taskId;
}

export function submitHandoffWithReceipt(request, credential) {
  return requestReceipt("handoffs", request, credential);
}

export function lookupHandoffReceipt(handoffId, credential) {
  return requestReceipt("handoffs/status", { handoffId }, credential);
}

async function requestReceipt(path, request, credential) {
  const result = await requestExtension(path, request, credential);
  if (typeof result.taskId !== "string" || result.taskId.length === 0) {
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
