import assert from "node:assert/strict";
import test from "node:test";

function browserEvent() {
  const listeners = new Set();
  return {
    addListener: (listener) => listeners.add(listener),
    hasListener: (listener) => listeners.has(listener),
    dispatch: (...args) => Promise.all([...listeners].map((listener) => listener(...args))),
  };
}

const url = "https://example.test/archive.zip";
const timeStamp = Date.now();
const get = { requestId: "get", url, method: "GET", tabId: 2, timeStamp: timeStamp + 10 };
const post = { requestId: "post", url, method: "POST", tabId: 1, timeStamp };
const attachment = {
  ...get,
  statusCode: 200,
  responseHeaders: [{ name: "Content-Disposition", value: "attachment" }],
};

async function startCapture(t) {
  const results = Promise.withResolvers();
  const storage = { captureSettings: { enabled: true, sites: [], types: [] }, pairingCredential: "fixture-token" };
  const local = {
    get: async (key) => ({ [key]: storage[key] }),
    getKeys: async () => Object.keys(storage),
    setAccessLevel: async () => {},
    set: async (entries) => {
      Object.assign(storage, entries);
      if (entries.captureResult) results.resolve(entries.captureResult);
    },
    remove: async (key) => { delete storage[key]; },
  };
  const transport = t.mock.method(globalThis, "fetch", async () => Response.json({ taskId: "saved" }, { status: 201 }));
  const webRequest = Object.fromEntries([
    "onBeforeRequest", "onSendHeaders", "onHeadersReceived", "onBeforeRedirect", "onCompleted", "onErrorOccurred",
  ].map((name) => [name, browserEvent()]));
  const downloads = {
    onCreated: browserEvent(),
    onDeterminingFilename: browserEvent(),
    cancel: t.mock.fn(async () => {}),
    search: async () => [{ state: "interrupted", error: "USER_CANCELED" }],
  };
  globalThis.chrome = {
    storage: { local, onChanged: browserEvent() },
    permissions: { contains: async () => true, onAdded: browserEvent(), onRemoved: browserEvent() },
    runtime: { onMessage: browserEvent() },
    action: { setBadgeBackgroundColor: async () => {}, setBadgeText: async () => {}, setTitle: async () => {} },
    i18n: { getMessage: (key) => key },
    downloads,
    webRequest,
  };
  t.after(() => { delete globalThis.chrome; });
  await import(`../../extension/capture.js?${encodeURIComponent(t.name)}`);
  return {
    webRequest, transport, downloads,
    async item(startedAt = timeStamp + 11) {
      await downloads.onDeterminingFilename.dispatch({
        id: 1, url, finalUrl: url, danger: "safe", filename: "archive.zip",
        startTime: new Date(startedAt).toISOString(),
      }, () => {});
      return results.promise;
    },
  };
}

for (const end of ["onCompleted", "onErrorOccurred"]) {
  test(`a same-URL POST ending at ${end} blocks a nearby GET match`, { timeout: 2500 }, async (t) => {
    const { webRequest, transport, downloads, item } = await startCapture(t);
    await webRequest.onBeforeRequest.dispatch(post);
    await webRequest[end].dispatch({ ...post, timeStamp: timeStamp + 1 });
    await webRequest.onBeforeRequest.dispatch(get);
    await webRequest.onHeadersReceived.dispatch(attachment);

    assert.equal(await item(), "captureUnsupported");
    assert.equal(transport.mock.callCount(), 0);
    assert.equal(downloads.cancel.mock.callCount(), 0);
  });
}

test("a terminal event without a navigation start still blocks a same-URL match", { timeout: 2500 }, async (t) => {
  const { webRequest, transport, item } = await startCapture(t);
  await webRequest.onErrorOccurred.dispatch({ ...post, timeStamp: timeStamp + 1 });
  await webRequest.onBeforeRequest.dispatch(get);
  await webRequest.onHeadersReceived.dispatch(attachment);

  assert.equal(await item(), "captureUnsupported");
  assert.equal(transport.mock.callCount(), 0);
});

test("an overlapping same-URL POST blocks the GET match while both are active", { timeout: 2500 }, async (t) => {
  const { webRequest, transport, item } = await startCapture(t);
  await webRequest.onBeforeRequest.dispatch(get);
  await webRequest.onHeadersReceived.dispatch(attachment);
  await webRequest.onBeforeRequest.dispatch({ ...post, timeStamp: timeStamp + 11 });

  assert.equal(await item(), "captureUnsupported");
  assert.equal(transport.mock.callCount(), 0);
});

test("a same-URL negative observation expires before a later GET", { timeout: 2500 }, async (t) => {
  const { webRequest, transport, item } = await startCapture(t);
  await webRequest.onBeforeRequest.dispatch({ ...post, timeStamp: timeStamp - 3001 });
  await webRequest.onErrorOccurred.dispatch({ ...post, timeStamp: timeStamp - 3000 });
  await webRequest.onBeforeRequest.dispatch(get);
  await webRequest.onHeadersReceived.dispatch(attachment);

  assert.equal(await item(), "captureAccepted");
  assert.equal(transport.mock.callCount(), 1);
});

test("a GET attachment without its navigation start is never proof", { timeout: 2500 }, async (t) => {
  const { webRequest, transport, item } = await startCapture(t);
  await webRequest.onHeadersReceived.dispatch(attachment);

  assert.equal(await item(), "captureUnsupported");
  assert.equal(transport.mock.callCount(), 0);
});

test("navigation tracking overflow leaves downloads with the browser", { timeout: 2500 }, async (t) => {
  const { webRequest, transport, item } = await startCapture(t);
  for (let index = 0; index < 257; index++) {
    await webRequest.onBeforeRequest.dispatch({
      ...get, requestId: `overflow-${index}`, url: `https://example.test/overflow-${index}`,
    });
  }
  await webRequest.onBeforeRequest.dispatch(get);
  await webRequest.onHeadersReceived.dispatch(attachment);

  assert.equal(await item(), "captureUnsupported");
  assert.equal(transport.mock.callCount(), 0);
});

test("an unmarked cookie download stays with the browser when replay is rejected", { timeout: 2500 }, async (t) => {
  const { webRequest, transport, downloads, item } = await startCapture(t);
  transport.mock.mockImplementation(async (_url, options) => {
    const request = JSON.parse(options.body);
    assert.match(request.captureProof, /^[0-9a-f]{64}$/);
    assert.equal(request.websiteSession, undefined);
    return Response.json({ error: { code: "CAPTURE_NOT_REPLAYABLE" } }, { status: 400 });
  });
  await webRequest.onBeforeRequest.dispatch(get);
  await webRequest.onSendHeaders.dispatch({ ...get, requestHeaders: [{ name: "Cookie", value: "fixture=private" }] });
  await webRequest.onHeadersReceived.dispatch(attachment);

  assert.equal(await item(), "captureSessionUnavailable");
  assert.equal(transport.mock.callCount(), 1);
  assert.equal(downloads.cancel.mock.callCount(), 0);
});
