import { pairingStatus, requestPairing } from "./pairing.js";

const get = (id) => document.getElementById(id);
const t = (key, substitutions) => chrome.i18n.getMessage(key, substitutions);
const status = get("status");
const details = get("details");
const confirmation = get("confirmation");
const requestId = get("request-id");
const code = get("code");
const button = get("pair");

document.documentElement.lang = t("@@ui_locale").replaceAll("_", "-");
get("code-label").textContent = t("approvalCode");
button.textContent = t("pair");
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
  button.hidden = pending || state === "connected";
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
await update(pairingStatus);
window.setInterval(() => { void update(pairingStatus); }, 2000);
