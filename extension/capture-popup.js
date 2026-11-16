import {
  captureHandoffPrefix, capturePermissions, captureResultKey, captureSettingsKey,
  normalizeExcludedSite, normalizeExcludedType, parseCaptureExclusions, readCaptureSettings,
} from "./capture-settings.js";

const get = (id) => document.getElementById(id);
const t = (key, substitutions) => chrome.i18n.getMessage(key, substitutions);
const form = get("capture-settings");
const enabled = get("capture-enabled");
const sites = get("capture-sites");
const types = get("capture-types");
const save = get("capture-save");
const settingsResult = get("capture-settings-result");
const result = get("capture-result");
const pending = get("capture-handoffs");

for (const [id, key] of [
  ["capture-settings-title", "captureSettingsTitle"],
  ["capture-enabled-label", "captureEnabled"],
  ["capture-help", "captureHelp"],
  ["capture-sites-label", "captureSites"],
  ["capture-sites-help", "captureSitesHelp"],
  ["capture-types-label", "captureTypes"],
  ["capture-types-help", "captureTypesHelp"],
  ["capture-save", "captureSave"],
]) get(id).textContent = t(key);

async function initialize() {
  try {
    const settings = await readCaptureSettings();
    enabled.checked = settings.enabled && await chrome.permissions.contains(capturePermissions);
    sites.value = settings.sites.join("\n");
    types.value = settings.types.join(", ");
    save.disabled = false;
  } catch {
    settingsResult.textContent = t("captureSettingsFailed");
  }
}

for (const input of [sites, types]) {
  input.addEventListener("input", () => input.setCustomValidity(""));
}

form.addEventListener("submit", async (event) => {
  event.preventDefault();
  const websiteExclusions = parseCaptureExclusions(sites.value, normalizeExcludedSite);
  const typeExclusions = parseCaptureExclusions(types.value, normalizeExcludedType);
  sites.setCustomValidity(websiteExclusions ? "" : t("captureInvalidSites"));
  types.setCustomValidity(typeExclusions ? "" : t("captureInvalidTypes"));
  if (!form.reportValidity()) return;

  save.disabled = true;
  settingsResult.textContent = "";
  try {
    // Permission requests must start in the user's submit gesture.
    const requested = enabled.checked;
    const allowed = !requested || await chrome.permissions.request(capturePermissions);
    const settings = { enabled: requested && allowed, sites: websiteExclusions, types: typeExclusions };
    await chrome.storage.local.set({ [captureSettingsKey]: settings });
    if (!requested) await chrome.permissions.remove(capturePermissions);
    enabled.checked = settings.enabled;
    settingsResult.textContent = t(allowed ? "captureSettingsSaved" : "capturePermissionDenied");
  } catch {
    settingsResult.textContent = t("captureSettingsFailed");
  } finally {
    save.disabled = false;
  }
});

function renderPending(key, record) {
  const row = document.createElement("li");
  const label = t("handoffRetryTarget", [record.host, new Date(record.createdAt).toLocaleString()]);
  row.textContent = label;
  const feedback = document.createElement("p");
  feedback.textContent = t(record.feedback ?? "captureUnconfirmed");
  row.append(feedback);
  const actions = document.createElement("div");
  const available = [];
  if (record.request) available.push(["retryCaptureHandoff", "captureRetry"]);
  available.push(["dismissCaptureHandoff", "handoffForget"]);
  for (const [type, keyLabel] of available) {
    const button = document.createElement("button");
    button.type = "button";
    button.textContent = t(keyLabel);
    button.setAttribute("aria-label", `${t(keyLabel)} ${label}`);
    button.dataset.captureAction = `${type}:${key}`;
    button.addEventListener("click", async () => {
      for (const button of actions.children) button.disabled = true;
      try {
        await chrome.runtime.sendMessage({ type, key });
      } catch {
        result.textContent = t("captureLocalFailure");
      } finally {
        for (const button of actions.children) button.disabled = false;
      }
    });
    actions.append(button);
  }
  row.append(actions);
  return row;
}

async function renderHandoffs() {
  const stored = await chrome.storage.local.get(null);
  const entries = Object.entries(stored)
    .filter(([key]) => key.startsWith(captureHandoffPrefix))
    .sort(([, a], [, b]) => b.createdAt - a.createdAt);
  const message = stored[captureResultKey];
  const content = message ? t(message) : "";
  if (result.textContent !== content) result.textContent = content;
  const focused = document.activeElement?.dataset.captureAction;
  pending.replaceChildren(...entries.map(([key, record]) => renderPending(key, record)));
  pending.hidden = entries.length === 0;
  if (focused) {
    const target = [...pending.querySelectorAll("button")].find((button) => button.dataset.captureAction === focused);
    target?.focus();
  }
}

function updateHandoffs() {
  void renderHandoffs().catch(() => { result.textContent = t("captureLocalFailure"); });
}

chrome.storage.onChanged.addListener((changes, area) => {
  if (area === "local" && Object.keys(changes).some((key) => key === captureResultKey || key.startsWith(captureHandoffPrefix))) {
    updateHandoffs();
  }
});
void initialize();
updateHandoffs();
