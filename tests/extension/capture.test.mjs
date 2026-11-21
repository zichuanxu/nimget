import assert from "node:assert/strict";
import test from "node:test";

function browserEvent() {
  const listeners = new Set();
  return {
    addListener: (listener) => listeners.add(listener),
    hasListener: (listener) => listeners.has(listener),
    dispatch: (...args) => [...listeners].map((listener) => listener(...args)),
  };
}

const url = "https://example.test/files/archive.zip";

async function startCapture(t, { stored = {}, cookies = [], cookieUrl = url, fetch, tabs = [] } = {}) {
  const storage = { pairingCredential: "fixture-token", ...stored };
  let settle;
  const outcome = new Promise((resolve) => { settle = resolve; });
  const events = [];
  const local = {
    get: async (key) => ({ [key]: storage[key] }),
    getKeys: async () => Object.keys(storage),
    setAccessLevel: async () => {},
    set: async (entries) => { Object.assign(storage, entries); },
    remove: async (keys) => { for (const key of [keys].flat()) delete storage[key]; },
  };
  const transport = t.mock.method(globalThis, "fetch", fetch ?? (async () => Response.json({ taskId: "saved" }, { status: 201 })));
  const downloads = {
    onDeterminingFilename: browserEvent(),
    cancel: t.mock.fn(async () => { events.push("cancel"); }),
    erase: t.mock.fn(async () => { events.push("erase"); }),
  };
  const webRequest = { onBeforeRequest: browserEvent() };
  globalThis.chrome = {
    storage: { local },
    permissions: { contains: async () => true },
    cookies: { getAll: async (query) => { assert.deepEqual(query, { url: cookieUrl }); return cookies; } },
    action: {
      setBadgeBackgroundColor: async () => {},
      setBadgeText: async () => {},
      setTitle: async ({ title }) => settle(title),
    },
    i18n: { getMessage: (key) => key },
    tabs: { query: async () => tabs, update: t.mock.fn(async () => { events.push("wake"); }) },
    downloads,
    webRequest,
  };
  t.after(() => { delete globalThis.chrome; });
  await import(`../../extension/capture.js?${encodeURIComponent(t.name)}`);
  return {
    transport, downloads, webRequest, storage, events, tabs: globalThis.chrome.tabs,
    async download(item = {}) {
      const suggest = t.mock.fn(() => { events.push("suggest"); });
      const [async] = downloads.onDeterminingFilename.dispatch({
        id: 7, url, finalUrl: url, referrer: "https://example.test/releases", danger: "safe",
        filename: "archive.zip", state: "in_progress", ...item,
      }, suggest);
      assert.equal(async, true);
      const result = await Promise.race([outcome, new Promise((resolve) => setTimeout(resolve, 1500))]);
      return { result, suggest };
    },
  };
}

test("capture is on by default and hands a browser download to NimGet", async (t) => {
  const { transport, events, download } = await startCapture(t);
  const { result, suggest } = await download();

  assert.equal(result, "captureAccepted");
  assert.equal(transport.mock.callCount(), 1);
  const [endpoint, init] = transport.mock.calls[0].arguments;
  assert.equal(String(endpoint), "http://127.0.0.1:51000/v1/extension/handoffs");
  const body = JSON.parse(init.body);
  assert.equal(body.url, url);
  assert.equal(body.captureProof, undefined);
  assert.equal(body.websiteSession, undefined);
  // Cancel before releasing the filename, so no Save As dialog appears.
  assert.deepEqual(events, ["cancel", "erase", "suggest"]);
  assert.equal(suggest.mock.callCount(), 1);
});

test("a download reached through a meta refresh is handed off from the file's URL", async (t) => {
  const page = "https://example.test/download.html?dd=files%2Freport.pdf";
  const file = "https://example.test/download.html?d=files/report.pdf";
  const { transport, download } = await startCapture(t, {
    cookieUrl: file,
    cookies: [{ name: "session", value: "fixture", expirationDate: Math.floor(Date.now() / 1000) + 600 }],
  });
  const { result } = await download({ url: page, finalUrl: file, referrer: page, filename: "report.pdf" });

  assert.equal(result, "captureAccepted");
  const body = JSON.parse(transport.mock.calls[0].arguments[1].body);
  assert.equal(body.url, file);
  assert.equal(body.websiteSession.cookie, "session=fixture");
});

test("the website's cookies travel with the download", async (t) => {
  const { transport, download } = await startCapture(t, {
    cookies: [{ name: "session", value: "fixture", expirationDate: Math.floor(Date.now() / 1000) + 600 }],
  });
  assert.equal((await download()).result, "captureAccepted");
  const { websiteSession } = JSON.parse(transport.mock.calls[0].arguments[1].body);
  assert.equal(websiteSession.cookie, "session=fixture");
  assert.equal(websiteSession.referer, "https://example.test/");
});

test("the browser keeps the download when NimGet isn't running", async (t) => {
  const { transport, downloads, events, download } = await startCapture(t, {
    fetch: async () => { throw new TypeError("connection refused"); },
  });
  const { result } = await download();

  assert.equal(result, "captureDesktopUnavailable");
  assert.equal(transport.mock.callCount(), 2, "submit, then one receipt lookup");
  assert.equal(downloads.cancel.mock.callCount(), 0);
  assert.deepEqual(events, ["suggest"]);
});

test("NimGet is woken through its link and then takes the download", async (t) => {
  let running = false;
  const { tabs, events, storage, download } = await startCapture(t, {
    tabs: [{ id: 4 }],
    fetch: async (endpoint) => {
      if (!running) throw new TypeError("connection refused");
      return String(endpoint).endsWith("/v1/ping") ? new Response("ok") : Response.json({ taskId: "saved" }, { status: 201 });
    },
  });
  tabs.update.mock.mockImplementation(async (id, { url: link }) => {
    assert.deepEqual([id, link], [4, "nimget://open"]);
    events.push("wake");
    running = true;
  });
  assert.equal((await download()).result, "captureAccepted");
  assert.deepEqual(events, ["wake", "cancel", "erase", "suggest"]);
  assert.equal(storage.wakeAttemptAt, undefined);
});

test("a failed wake is not repeated for every download", async (t) => {
  const { transport, tabs, download } = await startCapture(t, {
    stored: { wakeAttemptAt: Date.now() - 60_000 },
    tabs: [{ id: 4 }],
    fetch: async () => { throw new TypeError("connection refused"); },
  });
  assert.equal((await download()).result, "captureDesktopUnavailable");
  assert.equal(tabs.update.mock.callCount(), 0);
  assert.equal(transport.mock.callCount(), 2);
});

test("a lost reply is reconciled from the receipt", async (t) => {
  let calls = 0;
  const { downloads, download } = await startCapture(t, {
    fetch: async () => {
      if (calls++ === 0) throw new TypeError("reply lost");
      return Response.json({ taskId: "saved" }, { status: 200 });
    },
  });
  assert.equal((await download()).result, "captureAccepted");
  assert.equal(downloads.cancel.mock.callCount(), 1);
});

test("a rejected handoff leaves the download with the browser", async (t) => {
  const { downloads, events, download } = await startCapture(t, {
    fetch: async () => Response.json({ error: { code: "UNSUPPORTED_LINK" } }, { status: 400 }),
  });
  assert.equal((await download()).result, "captureUnsupported");
  assert.equal(downloads.cancel.mock.callCount(), 0);
  assert.deepEqual(events, ["suggest"]);
});

// Routes extension API calls by path; each route returns [status, body].
function desktop(routes) {
  const calls = [];
  const fetch = async (endpoint) => {
    const path = new URL(endpoint).pathname.replace("/v1/extension/", "");
    calls.push(path);
    const [status, body] = routes[path](calls.filter((call) => call === path).length);
    return Response.json(body, { status });
  };
  return { calls, fetch };
}

test("an unpaired browser pairs on its first download and hands it off", async (t) => {
  const api = desktop({
    pairing: () => [200, { id: "P1", status: "connected" }],
    handoffs: () => [201, { taskId: "saved" }],
  });
  const { download, storage, events } = await startCapture(t, { fetch: api.fetch });
  delete storage.pairingCredential;
  assert.equal((await download()).result, "captureAccepted");
  assert.deepEqual(api.calls, ["pairing", "handoffs"]);
  assert.match(storage.pairingCredential, /^[0-9a-f]{64}$/);
  assert.deepEqual(events, ["cancel", "erase", "suggest"]);
});

test("a browser NimGet has not approved keeps its downloads", async (t) => {
  const api = desktop({ pairing: () => [200, { id: "P1", status: "awaiting-approval", code: "ABCD1234" }] });
  const { download, storage, events } = await startCapture(t, { fetch: api.fetch });
  delete storage.pairingCredential;
  assert.equal((await download()).result, "capturePairingRequired");
  assert.deepEqual(api.calls, ["pairing"]);
  assert.deepEqual(events, ["suggest"]);
});

test("a revoked browser stays revoked and keeps its downloads", async (t) => {
  const api = desktop({
    handoffs: () => [403, { error: { code: "PAIRING_REQUIRED" } }],
    "pairing/status": () => [200, { id: "P1", status: "revoked" }],
  });
  const { download, storage } = await startCapture(t, { fetch: api.fetch });
  assert.equal((await download()).result, "capturePairingRequired");
  assert.deepEqual(api.calls, ["handoffs", "pairing/status"]);
  assert.equal(storage.pairingCredential, "fixture-token");
});

test("a browser whose pairing was deleted pairs again and hands off", async (t) => {
  const api = desktop({
    handoffs: (n) => n === 1 ? [403, { error: { code: "PAIRING_REQUIRED" } }] : [201, { taskId: "saved" }],
    "pairing/status": () => [200, { status: "disconnected" }],
    pairing: () => [200, { id: "P2", status: "connected" }],
  });
  const { download, storage } = await startCapture(t, { fetch: api.fetch });
  assert.equal((await download()).result, "captureAccepted");
  assert.deepEqual(api.calls, ["handoffs", "pairing/status", "pairing", "handoffs"]);
  assert.notEqual(storage.pairingCredential, "fixture-token");
});

const skipped = [
  ["turned off", { stored: { captureSettings: { enabled: false, sites: [], types: [] } } }, {}],
  ["an excluded site", { stored: { captureSettings: { enabled: true, sites: ["example.test"], types: [] } } }, {}],
  ["an excluded type", { stored: { captureSettings: { enabled: true, sites: [], types: ["zip"] } } }, {}],
  ["incognito", {}, { incognito: true }],
  ["another extension's download", {}, { byExtensionId: "other" }],
  ["a blob URL", {}, { url: "blob:https://example.test/1", finalUrl: "blob:https://example.test/1" }],
];
for (const [name, options, item] of skipped) {
  test(`capture skips ${name}`, async (t) => {
    const { transport, download } = await startCapture(t, options);
    const { suggest } = await download(item);
    assert.equal(transport.mock.callCount(), 0);
    assert.equal(suggest.mock.callCount(), 1);
  });
}

test("capture skips a download that answered a form POST", async (t) => {
  const { transport, webRequest, download } = await startCapture(t);
  webRequest.onBeforeRequest.dispatch({ url, method: "POST", timeStamp: Date.now() });
  const { suggest } = await download();
  assert.equal(transport.mock.callCount(), 0);
  assert.equal(suggest.mock.callCount(), 1);
});

test("stale handoff notices from earlier versions are removed", async (t) => {
  const { storage } = await startCapture(t, { stored: { "captureHandoff:1:2026-01-01T00:00:00Z": { host: "example.test" } } });
  await new Promise((resolve) => setTimeout(resolve, 20));
  assert.equal(Object.keys(storage).some((key) => key.startsWith("captureHandoff:")), false);
});
