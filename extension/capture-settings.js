export const captureHandoffPrefix = "captureHandoff:";
export const captureResultKey = "captureResult";
export const captureSettingsKey = "captureSettings";
export const capturePermissions = {
  permissions: ["downloads"],
  origins: ["http://*/*", "https://*/*"],
};

export async function readCaptureSettings() {
  const stored = await chrome.storage.local.get(captureSettingsKey);
  return stored[captureSettingsKey] ?? { enabled: false, sites: [], types: [] };
}

export function parseCaptureExclusions(text, normalize) {
  const entries = text.split(/[\s,]+/).filter(Boolean).map(normalize);
  if (entries.includes(null)) return null;
  return [...new Set(entries)];
}

export function normalizeExcludedSite(entry) {
  if (!/^[\p{L}\p{N}.[\]:-]+$/u.test(entry) || (!entry.startsWith("[") && entry.includes(":"))) return null;
  try {
    const hostname = new URL("https://" + entry).hostname.replace(/\.$/, "");
    return hostname && !hostname.startsWith(".") && !hostname.includes("..") ? hostname : null;
  } catch {
    return null;
  }
}

export function normalizeExcludedType(entry) {
  const type = entry.toLowerCase().replace(/^\./, "");
  return /^[a-z0-9][a-z0-9_+-]*(?:\.[a-z0-9][a-z0-9_+-]*)*$/.test(type) ? type : null;
}
