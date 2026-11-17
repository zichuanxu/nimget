import { renderRetryList } from "./retry-list.js";
import { ensurePaired, pairingStatus, requestPairing } from "./pairing.js";
import { manualHandoffPrefix, manualHandoffResult } from "./handoff.js";
import { launchDesktop } from "./wake.js";

const get = (id) => document.getElementById(id);
const t = (key, substitutions) => chrome.i18n.getMessage(key, substitutions);
const status = get("status");
const details = get("details");
const confirmation = get("confirmation");
const requestId = get("request-id");
const code = get("code");
const button = get("pair");
const openDesktop = get("open-desktop");

document.documentElement.lang = t("@@ui_locale").replaceAll("_", "-");
get("code-label").textContent = t("approvalCode");
button.textContent = t("pair");
openDesktop.textContent = t("openDesktop");
status.textContent = t("checking");

const labels = {
  disconnected: "disconnected",
  "awaiting-approval": "pending",
  connected: "connected",
  denied: "denied",
  revoked: "revoked",
};

function setText(element, value) {
  if (element.textContent !== value) element.textContent = value;
}

function render(result, failure) {
  const state = labels[result.status] ?? "disconnected";
  const pending = state === "pending";
  setText(status, t(state));
  setText(details, t(failure ?? `${state}Detail`));
  confirmation.hidden = !pending;
  setText(requestId, pending ? t("requestId", result.id.slice(-6)) : "");
  setText(code, pending ? result.code : "");
  const unreachable = failure === "unreachable";
  openDesktop.hidden = !unreachable;
  button.hidden = unreachable || pending || state === "connected";
}

let busy = false;
let requestFailure;
async function update(action, requesting = false) {
  if (busy) return;
  busy = true;
  try {
    const result = await action();
    if (requesting || result.status !== "disconnected") requestFailure = undefined;
    render(result, requestFailure);
  } catch (err) {
    const failure = {
      DESKTOP_UNREACHABLE: "unreachable",
      PAIRING_LIMIT: "pairingLimit",
    }[err.code] ?? "failed";
    if (requesting) requestFailure = err.code === "DESKTOP_UNREACHABLE" ? undefined : failure;
    render({ status: "disconnected" }, failure);
  } finally {
    busy = false;
  }
}

button.addEventListener("click", async () => {
  button.disabled = true;
  await update(requestPairing, true);
  button.disabled = false;
});

// The browser asks before opening NimGet. Status polling shows the result.
openDesktop.addEventListener("click", () => { void launchDesktop(); });

const handoffResult = get("handoff-result");
const retries = get("handoff-retries");

async function renderHandoffs() {
  const stored = await chrome.storage.local.get(null);
  const records = Object.entries(stored)
    .filter(([key]) => key.startsWith(manualHandoffPrefix))
    .map(([, record]) => record)
    .sort((a, b) => b.createdAt - a.createdAt);
  const feedback = stored[manualHandoffResult] ?? (records.length > 0 ? "handoffUnconfirmed" : null);
  setText(handoffResult, feedback ? t(feedback) : "");
  renderRetryList(retries, records.map(renderHandoffRetry));
}

function renderHandoffRetry(record) {
  const item = document.createElement("li");
  const host = new URL(record.request.url).hostname;
  const date = new Date(record.createdAt).toLocaleString();
  const label = t("handoffRetryTarget", [host, date]);
  item.textContent = label;
  if (record.feedback) {
    const feedback = document.createElement("p");
    feedback.textContent = t(record.feedback);
    item.append(feedback);
  }
  const actions = document.createElement("div");
  for (const [type, key] of [["retryManualHandoff", "handoffRetry"], ["forgetManualHandoff", "handoffForget"]]) {
    const button = document.createElement("button");
    button.type = "button";
    button.textContent = t(key);
    button.setAttribute("aria-label", `${t(key)} ${label}`);
    button.dataset.retryAction = `${type}:${record.request.handoffId}`;
    button.addEventListener("click", async () => {
      for (const button of actions.children) button.disabled = true;
      try {
        await chrome.runtime.sendMessage({ type, handoffId: record.request.handoffId });
      } catch {
        setText(handoffResult, t("handoffLocalFailure"));
      } finally {
        for (const button of actions.children) button.disabled = false;
      }
    });
    actions.append(button);
  }
  item.append(actions);
  return item;
}

function updateHandoffs() {
  void renderHandoffs().catch(() => setText(handoffResult, t("handoffLocalFailure")));
}

chrome.storage.onChanged.addListener((changes, area) => {
  if (area === "local" && Object.keys(changes).some((key) => key === manualHandoffResult || key.startsWith(manualHandoffPrefix))) {
    updateHandoffs();
  }
});
updateHandoffs();

// Opening the popup connects an unpaired extension.
await update(ensurePaired, true);
window.setInterval(() => { void update(pairingStatus); }, 2000);
