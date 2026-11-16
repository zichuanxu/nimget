const draftPrefix = "websiteSessionDraft:";
const snapshotPrefix = "websiteSessionSnapshot:";
const draftLifetime = 15 * 60 * 1000;
const snapshotLifetime = 24 * 60 * 60;

export const websiteSessionPrompt = "website-session.html";

export function websiteSessionPermission(link) {
  const { protocol, hostname } = new URL(link);
  // Chromium checks permission against a cookie's host and its Secure flag,
  // without the download URL's port. HTTPS can use both kinds of cookies.
  const origins = [protocol + "//" + hostname + "/*"];
  if (protocol === "https:") origins.push("http://" + hostname + "/*");
  return { permissions: ["cookies"], origins };
}

// Call directly from an explicit permission button's click handler.
export function requestWebsiteSessionPermission(link) {
  return chrome.permissions.request(websiteSessionPermission(link));
}

// A read-only check also works outside a user gesture.
export function hasWebsiteSessionPermission(link) {
  return chrome.permissions.contains(websiteSessionPermission(link));
}

export async function createWebsiteSessionDraft(handoffId, info, tabId, scope) {
  const { origin } = new URL(info.linkUrl);
  const sameOrigin = (link) => {
    try {
      return new URL(link).origin === origin;
    } catch {
      return false;
    }
  };
  // Include only an origin referrer for same-origin navigation.
  const referer = sameOrigin(info.pageUrl) && (!info.frameUrl || sameOrigin(info.frameUrl)) ? origin + "/" : undefined;
  const draft = { url: info.linkUrl, referer, tabId, scope, createdAt: Date.now() };
  await chrome.storage.session.set({ [draftPrefix + handoffId]: draft });
}

export async function readWebsiteSessionDraft(handoffId) {
  const key = draftPrefix + handoffId;
  const { [key]: draft } = await chrome.storage.session.get(key);
  if (draft && Date.now() - draft.createdAt < draftLifetime) return draft;
  await chrome.storage.session.remove(key);
  return null;
}

export function removeWebsiteSessionDraft(handoffId) {
  return chrome.storage.session.remove(draftPrefix + handoffId);
}

export async function readWebsiteSession({ url, referer, tabId }) {
  if (!await hasWebsiteSessionPermission(url)) return null;
  const stores = await chrome.cookies.getAllCookieStores();
  const store = stores.find(({ tabIds }) => tabIds.includes(tabId));
  if (!store) return null;
  const candidates = await chrome.cookies.getAll({ url, storeId: store.id });
  const now = Math.floor(Date.now() / 1000);
  const secure = new URL(url).protocol === "https:";
  const cookies = [];
  let expiresAt = now + snapshotLifetime;
  for (const cookie of candidates) {
    if (cookie.partitionKey || (cookie.secure && !secure) || (cookie.sameSite === "strict" && !referer)) continue;
    if (cookie.expirationDate !== undefined && Math.floor(cookie.expirationDate) <= now) continue;
    if (!validCookie(cookie)) continue;
    cookies.push(`${cookie.name}=${cookie.value}`);
    if (cookie.expirationDate !== undefined) expiresAt = Math.min(expiresAt, Math.floor(cookie.expirationDate));
  }
  if (cookies.length === 0) return null;
  return { cookie: cookies.join("; "), referer, userAgent: navigator.userAgent, expiresAt };
}

function validCookie({ name, value }) {
  // Browser storage can contain values that cannot safely form a Cookie
  // header. Keep valid quoted values and reject delimiters inside a value.
  const unquoted = value.startsWith('"') && value.endsWith('"') ? value.slice(1, -1) : value;
  return /^[!#$%&'*+.^_`|~0-9A-Za-z-]+$/.test(name) && /^[\x21\x23-\x2b\x2d-\x3a\x3c-\x5b\x5d-\x7e]*$/.test(unquoted);
}

export function saveWebsiteSessionSnapshot(handoffId, session) {
  return chrome.storage.session.set({ [snapshotPrefix + handoffId]: session });
}

export async function readWebsiteSessionSnapshot(handoffId) {
  const key = snapshotPrefix + handoffId;
  const { [key]: session } = await chrome.storage.session.get(key);
  if (session && session.expiresAt > Math.floor(Date.now() / 1000)) return session;
  await chrome.storage.session.remove(key);
  return null;
}

export function removeWebsiteSessionSnapshot(handoffId) {
  return chrome.storage.session.remove(snapshotPrefix + handoffId);
}
