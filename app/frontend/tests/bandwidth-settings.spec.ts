import { expect, test } from "@playwright/test";
import { mockDesktop } from "./desktop";

test("edits the shared magnet upload limit alongside the download limit", async ({ page }) => {
  let stored: unknown = {
    maxConcurrent: 3, connections: 8, hostConnections: 16, speedLimit: 512 * 1024,
    torrentUploadLimit: 64 * 1024, desktop: { language: "en" },
  };
  let saves = 0;
  await mockDesktop(page, {
    Settings: () => stored,
    WhenDone: () => "",
    SaveSettings: (next) => {
      saves++;
      return (stored = next);
    },
  });

  await page.goto("/");
  const dialog = page.getByRole("dialog", { name: "Settings" });
  async function openDownloads() {
    await page.getByRole("button", { name: "Settings" }).press("Enter");
    await dialog.getByRole("button", { name: "Downloads", exact: true }).press("Enter");
  }
  await openDownloads();
  const upload = dialog.getByLabel("BitTorrent upload limit");
  const save = dialog.getByRole("button", { name: "Save", exact: true });
  await expect(upload).toHaveValue("64K");
  await expect(upload).toHaveAccessibleDescription(/all active magnet downloads/i);
  await expect(dialog.getByLabel("Connections per download", { exact: true }))
    .toHaveAccessibleDescription(/HTTP/i);
  await expect(dialog.getByLabel("Total download limit")).toHaveValue("512K");

  await upload.fill("0");
  await save.press("Enter");
  await expect(upload).toBeFocused();
  await expect(upload).toHaveAttribute("aria-invalid", "true");
  expect(saves).toBe(0);

  await upload.fill("32K");
  await save.press("Enter");
  await expect(dialog).toHaveCount(0);
  expect(stored).toMatchObject({ torrentUploadLimit: 32768, speedLimit: 524288, connections: 8, hostConnections: 16 });
  await openDownloads();
  await expect(upload).toHaveValue("32K");
});
