import {
  captureResultKey, captureSettingsKey, normalizeExcludedSite, normalizeExcludedType, parseCaptureExclusions,
  readCaptureSettings,
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
    enabled.checked = settings.enabled;
    sites.value = settings.sites.join("\n");
    types.value = settings.types.join(", ");
    save.disabled = false;
  } catch {
    settingsResult.textContent = t("captureSettingsFailed");
  }
}

async function store(settings) {
  settingsResult.textContent = "";
  try {
    await chrome.storage.local.set({ [captureSettingsKey]: settings });
    settingsResult.textContent = t("captureSettingsSaved");
  } catch {
    settingsResult.textContent = t("captureSettingsFailed");
  }
}

for (const input of [sites, types]) {
  input.addEventListener("input", () => input.setCustomValidity(""));
}

// The switch takes effect at once; exclusions are saved with the button.
enabled.addEventListener("change", async () => {
  const settings = await readCaptureSettings();
  await store({ ...settings, enabled: enabled.checked });
});

form.addEventListener("submit", async (event) => {
  event.preventDefault();
  const websiteExclusions = parseCaptureExclusions(sites.value, normalizeExcludedSite);
  const typeExclusions = parseCaptureExclusions(types.value, normalizeExcludedType);
  sites.setCustomValidity(websiteExclusions ? "" : t("captureInvalidSites"));
  types.setCustomValidity(typeExclusions ? "" : t("captureInvalidTypes"));
  if (!form.reportValidity()) return;
  save.disabled = true;
  await store({ enabled: enabled.checked, sites: websiteExclusions, types: typeExclusions });
  save.disabled = false;
});

function renderResult(message) {
  const content = message ? t(message) : "";
  if (result.textContent !== content) result.textContent = content;
}

chrome.storage.onChanged.addListener((changes, area) => {
  if (area === "local" && changes[captureResultKey]) renderResult(changes[captureResultKey].newValue);
});
void initialize();
void chrome.storage.local.get(captureResultKey).then((stored) => renderResult(stored[captureResultKey]), () => {});
