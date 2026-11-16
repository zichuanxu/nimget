import assert from "node:assert/strict";
import test from "node:test";
import { hasWebsiteSessionPermission, readWebsiteSession, requestWebsiteSessionPermission } from "../../extension/website-session.js";

const url = "https://downloads.example.test:8443/files/report.zip";
const expected = { permissions: ["cookies"], origins: ["https://downloads.example.test/*", "http://downloads.example.test/*"] };

test("permission requests and checks are scoped to the target", async () => {
  globalThis.chrome = {
    permissions: {
      request: async (permission) => {
        assert.deepEqual(permission, expected);
        return true;
      },
      contains: async (permission) => {
        assert.deepEqual(permission, expected);
        return false;
      },
    },
  };
  assert.equal(await requestWebsiteSessionPermission(url), true);
  assert.equal(await hasWebsiteSessionPermission(url), false);
  assert.equal(await readWebsiteSession({ url, tabId: 3 }), null);
});

test("only applicable, unexpired cookies from the originating tab store travel with a link", async () => {
  const expiresAt = Math.floor(Date.now() / 1000) + 120;
  globalThis.chrome = {
    permissions: { contains: async () => true },
    cookies: {
      getAllCookieStores: async () => [{ id: "0", tabIds: [1, 2] }, { id: "1", tabIds: [3] }],
      getAll: async (query) => {
        assert.deepEqual(query, { url, storeId: "1" });
        return [
          { name: "path", value: "one", expirationDate: expiresAt + 100 },
          { name: "session", value: "two", expirationDate: expiresAt + 0.5, httpOnly: true, secure: true },
          { name: "strict", value: "skip", sameSite: "strict" },
          { name: "partitioned", value: "skip", partitionKey: {} },
          { name: "expired", value: "skip", expirationDate: expiresAt - 1200 },
          { name: "bad", value: "skip; injected=yes" },
        ];
      },
    },
  };
  const session = await readWebsiteSession({ url, tabId: 3 });
  assert.equal(session.cookie, "path=one; session=two");
  assert.equal(session.expiresAt, expiresAt);
  assert.equal(session.referer, undefined);
  assert.equal(await readWebsiteSession({ url, tabId: 4 }), null);
});
