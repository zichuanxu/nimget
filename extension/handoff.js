import { ExtensionRequestError, requestExtension } from "./pairing.js";

export const manualHandoffPrefix = "manualHandoff:";
export const manualHandoffResult = "manualHandoffResult";

export async function submitHandoff(request, credential) {
  const result = await requestExtension("handoffs", request, credential);
  if (typeof result.taskId !== "string" || result.taskId.length === 0) {
    throw new ExtensionRequestError("HANDOFF_UNCONFIRMED");
  }
  return result.taskId;
}

export function supportsHandoff(link) {
  try {
    const url = new URL(link);
    return (url.protocol === "http:" || url.protocol === "https:") && !url.username && !url.password;
  } catch {
    return false;
  }
}
