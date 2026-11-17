// The extension cannot start programs. The NimGet installer registers the
// nimget:// link, so the browser can hand it to the system, which starts
// NimGet or shows its running window.
const pingURL = "http://127.0.0.1:51000/v1/ping";
const wakeKey = "wakeAttemptAt";
// The browser asks before opening an app link. After a declined or failed
// attempt, leave downloads with the browser instead of asking each time.
const retryAfterMs = 10 * 60 * 1000;

export async function desktopRunning() {
  try {
    const response = await fetch(pingURL, {
      cache: "no-store", credentials: "omit", redirect: "error", signal: AbortSignal.timeout(1000),
    });
    return response.ok;
  } catch {
    return false;
  }
}

export async function launchDesktop() {
  const [tab] = await chrome.tabs.query({ active: true, lastFocusedWindow: true });
  if (tab?.id === undefined) return false;
  try {
    await chrome.tabs.update(tab.id, { url: "nimget://open" });
    return true;
  } catch {
    return false;
  }
}

export async function waitForDesktop(timeoutMs = 15_000) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    if (await desktopRunning()) return true;
    await new Promise((resolve) => setTimeout(resolve, 500));
  }
  return false;
}

export async function wakeDesktop() {
  const { [wakeKey]: last } = await chrome.storage.local.get(wakeKey);
  if (typeof last === "number" && Date.now() - last < retryAfterMs) return false;
  await chrome.storage.local.set({ [wakeKey]: Date.now() });
  if (!await launchDesktop() || !await waitForDesktop()) return false;
  await chrome.storage.local.remove(wakeKey);
  return true;
}
