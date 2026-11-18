import { expect, test } from "@playwright/test";
import { mockDesktop } from "./desktop";

const release = {
  version: "v1.5.0",
  url: "https://github.com/amemiya02/nimget/releases/tag/v1.5.0",
  package: { name: "NimGet-v1.5.0-windows-x64-setup.exe", digest: "sha256:" + "a".repeat(64) },
};

test("queues the offered update from the automatic notice", async ({ page }) => {
  const items: unknown[] = [];
  let requested: unknown;
  await mockDesktop(page, {
    State: () => ({ version: "v1.4.1", canCheckUpdates: true, update: release }),
    List: () => items,
    DownloadUpdate: (version) => {
      requested = version;
      const item = { id: "update", destination: release.package.name, status: "queued", createdAt: "2026-11-18T08:00:00Z" };
      items.push(item);
      return item;
    },
  });
  await page.goto("/");
  await expect(page.getByText("NimGet v1.5.0 is available.")).toBeVisible();
  await page.getByRole("button", { name: "Download update" }).click();
  await expect(page.getByText("Update package is in your download queue.")).toBeVisible();
  await expect(page.getByText(release.package.name, { exact: true })).toBeVisible();
  expect(requested).toBe(release.version);
});

test("checks manually even when automatic checks are off and explains missing packages", async ({ page }) => {
  let checks = 0;
  let requested: unknown;
  await mockDesktop(page, {
    State: () => ({ version: "v1.4.1", canCheckUpdates: true }),
    Settings: () => ({ desktop: { language: "en", checkUpdates: false }, schedule: {} }),
    WhenDone: () => "",
    CheckForUpdate: () => {
      switch (++checks) {
        case 1: throw new Error("offline");
        case 2: return {};
        case 3: return { update: { version: release.version, url: release.url } };
        default: return { update: release };
      }
    },
    DownloadUpdate: (version) => {
      requested = version;
      return { id: "update" };
    },
  });
  await page.goto("/");
  await page.getByRole("button", { name: /Settings/ }).click();
  const settings = page.getByRole("dialog", { name: "Settings" });
  const check = settings.getByRole("button", { name: "Check now" });
  await check.click();
  await expect(settings.getByRole("alert")).toContainText("Couldn’t check for updates");
  await check.click();
  await expect(settings.getByText("NimGet is up to date.")).toBeVisible();
  await check.click();
  await expect(settings.getByText("NimGet v1.5.0 is available.")).toBeVisible();
  await expect(settings.getByText("No verified package is available for this platform.")).toBeVisible();
  await expect(settings.getByRole("button", { name: "Download update" })).toHaveCount(0);
  await expect(settings.getByRole("button", { name: "View release" })).toBeVisible();
  await check.click();
  await settings.getByRole("button", { name: "Download update" }).click();
  await expect(settings).toBeHidden();
  await expect(page.getByText("Update package is in your download queue.")).toBeVisible();
  expect(requested).toBe(release.version);
});
