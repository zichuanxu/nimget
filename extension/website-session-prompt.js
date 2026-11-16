import { readWebsiteSessionDraft, requestWebsiteSessionPermission } from "./website-session.js";

const get = (id) => document.getElementById(id);
const t = (key, substitutions) => chrome.i18n.getMessage(key, substitutions);
const useSession = get("use-website-session");
const withoutSession = get("send-without-session");
const choice = get("website-session-choice");
const result = get("website-session-result");
const handoffId = location.hash.slice(1);
document.documentElement.lang = t("@@ui_locale").replaceAll("_", "-");
get("website-session-title").textContent = t("websiteSessionTitle");
document.title = t("websiteSessionTitle");

async function showDraft() {
  const draft = await readWebsiteSessionDraft(handoffId);
  if (!draft) {
    result.textContent = t("websiteSessionDraftExpired");
    return;
  }
  get("website-session-description").textContent = t("websiteSessionDescription", new URL(draft.url).host);
  useSession.textContent = t("websiteSessionUse");
  withoutSession.textContent = t("websiteSessionSkip");
  choice.hidden = false;
  useSession.addEventListener("click", () => void submit(draft, true));
  withoutSession.addEventListener("click", () => void submit(draft, false));
}

async function submit(draft, useWebsiteSession) {
  useSession.disabled = true;
  withoutSession.disabled = true;
  try {
    if (useWebsiteSession && !await requestWebsiteSessionPermission(draft.url)) {
      result.textContent = t("websiteSessionDenied");
      return;
    }
    result.textContent = t("websiteSessionSending");
    const feedback = await chrome.runtime.sendMessage({ type: "submitWebsiteSessionHandoff", handoffId, useWebsiteSession });
    result.textContent = t(feedback ?? "handoffLocalFailure");
    if (feedback !== "websiteSessionUnavailable") {
      choice.hidden = true;
    }
  } catch {
    result.textContent = t("websiteSessionUnavailable");
  } finally {
    useSession.disabled = false;
    withoutSession.disabled = false;
  }
}

await showDraft().catch(() => { result.textContent = t("handoffLocalFailure"); });
