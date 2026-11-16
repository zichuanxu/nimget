import { expect, test } from "@playwright/test";
import { mockDesktop } from "./desktop";

for (const [url, sameOrigin] of [
  ["http://example.test/archive.zip", false],
  ["https://example.test:8443/archive.zip", false],
  ["https://example.test:443/archive.zip", true],
] as const) {
  test(`pasted cURL headers respect the effective origin of ${url}`, async ({ page }) => {
    const source = "https://example.test/original.zip";
    const headers = { Cookie: "fixture=bound", Authorization: "Bearer fixture" };
    const requests: unknown[] = [];
    await mockDesktop(page, {
      ParseCurl: () => ({ url: source, headers }),
      Add: (request) => { requests.push(request); return "queued"; },
    });
    await page.goto("/");
    await page.getByRole("button", { name: "New download", exact: true }).first().click();
    const dialog = page.getByRole("dialog", { name: "New download" });
    const links = dialog.getByRole("textbox", { name: /^Links/ });
    await links.fill(`curl '${source}' -H 'Cookie: fixture=bound'`);
    await expect(links).toHaveValue(source);
    await links.fill(url);
    await dialog.getByRole("button", { name: "Download", exact: true }).click();
    await expect.poll(() => requests.length).toBe(1);
    expect(requests[0]).toMatchObject({ url });
    if (sameOrigin) expect(requests[0]).toHaveProperty("headers", headers);
    else expect(requests[0]).not.toHaveProperty("headers", headers);
  });
}

