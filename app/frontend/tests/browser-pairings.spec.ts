import { expect, test, type Page } from "@playwright/test";
import { mockDesktop } from "./desktop";

async function openBrowserSettings(page: Page) {
  await page.getByRole("button", { name: "Settings" }).press("Enter");
  const dialog = page.getByRole("dialog", { name: "Settings" });
  await dialog.getByRole("button", { name: "Browser extensions" }).press("Enter");
  return dialog;
}

test("saves automatic extension connection with the other settings", async ({ page }) => {
  let stored: unknown = { connections: 8, schedule: {}, desktop: { language: "en" }, extensions: { autoConnect: true } };
  await mockDesktop(page, {
    Settings: () => stored,
    WhenDone: () => "",
    SaveSettings: (next) => (stored = next),
    ExtensionPairings: () => [],
  });
  await page.goto("/");
  const dialog = await openBrowserSettings(page);
  const autoConnect = dialog.getByRole("checkbox", { name: /Connect the NimGet extension automatically/ });
  await expect(autoConnect).toBeChecked();
  await autoConnect.uncheck();
  await dialog.getByRole("button", { name: "Save", exact: true }).press("Enter");
  await expect(dialog).toHaveCount(0);
  expect(stored).toMatchObject({ extensions: { autoConnect: false }, connections: 8 });
});

test("deletes only ended pairings", async ({ page }) => {
  let records = [
    { id: "CONNECTED0001", browser: "chrome", status: "connected" },
    { id: "REVOKED000002", browser: "chrome", status: "revoked" },
    { id: "DENIED0000003", browser: "edge", status: "denied" },
  ];
  const deleted: string[] = [];
  await mockDesktop(page, {
    WhenDone: () => "",
    ExtensionPairings: () => records,
    DeleteExtensionPairing: (id) => {
      deleted.push(id as string);
      records = records.filter((record) => record.id !== id);
    },
  });
  await page.goto("/");
  const dialog = await openBrowserSettings(page);
  const rows = dialog.getByRole("listitem");
  await expect(rows).toHaveCount(3);
  await expect(rows.nth(0).getByRole("button", { name: "Delete" })).toHaveCount(0);
  await expect(rows.nth(0).getByRole("button", { name: "Revoke access" })).toBeVisible();

  await rows.nth(1).getByRole("button", { name: "Delete" }).press("Enter");
  await expect(rows).toHaveCount(2);
  await rows.nth(1).getByRole("button", { name: "Delete" }).press("Enter");
  await expect(rows).toHaveCount(1);
  expect(deleted).toEqual(["REVOKED000002", "DENIED0000003"]);
});
