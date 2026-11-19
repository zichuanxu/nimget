import { expect, test } from "@playwright/test";
import { mockDesktop } from "./desktop";

const release = {
  version: "v1.5.0",
  url: "https://github.com/amemiya02/nimget/releases/tag/v1.5.0",
  package: { name: "NimGet-v1.5.0-windows-x64-setup.exe", digest: "sha256:" + "a".repeat(64) },
};

test("checks and queues a verified update from the main window", async ({ page }) => {
  let checks = 0;
  let requested: unknown;
  let finishCheck!: () => void;
  const checking = new Promise<void>((resolve) => { finishCheck = resolve; });
  const items: unknown[] = [];

  await mockDesktop(page, {
    State: () => ({ version: "v1.4.1", canCheckUpdates: true }),
    Settings: () => ({ desktop: { language: "en", checkUpdates: false } }),
    List: () => items,
    CheckForUpdate: async () => {
      switch (++checks) {
        case 1: throw new Error("offline");
        case 2: return {};
        default:
          await checking;
          return { update: release };
      }
    },
    DownloadUpdate: (version) => {
      requested = version;
      const item = { id: "update", destination: release.package.name, status: "queued", createdAt: "2026-11-18T08:00:00Z" };
      items.push(item);
      return item;
    },
  });

  await page.goto("/");
  const open = page.getByRole("button", { name: "Check for updates" });
  await expect(open).toBeVisible({ timeout: 3000 });
  await open.focus();
  await open.press("Enter");
  const updates = page.getByRole("dialog", { name: "Updates" });
  const check = updates.getByRole("button", { name: "Check now" });
  await expect(updates.getByRole("alert")).toContainText("Couldn’t check for updates");
  await check.click();
  await expect(updates.getByRole("status")).toHaveText("NimGet is up to date.");

  await check.click();
  await expect(check).toBeDisabled();
  await expect(check).toBeFocused();
  await expect(updates.getByText("Checking for updates…")).toBeVisible();
  finishCheck();
  await expect(updates.getByRole("status")).toContainText("NimGet v1.5.0 is available.");
  expect(requested).toBeUndefined();

  await updates.getByRole("button", { name: "Download update" }).click();
  await expect(updates).toBeHidden();
  await expect(open).toBeFocused();
  await expect(page.getByText(release.package.name, { exact: true })).toBeVisible();
  expect(requested).toBe(release.version);
});

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
