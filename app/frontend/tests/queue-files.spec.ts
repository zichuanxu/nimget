import { expect, test } from "@playwright/test";
import { mockDesktop } from "./desktop";

test("exports a safe summary and explicitly confirms a source backup", async ({ page }) => {
  const calls: string[] = [];
  await mockDesktop(page, {
    ExportQueue: () => { calls.push("summary"); return "summary.json"; },
    BackupQueue: () => { calls.push("backup"); return "backup.json"; },
  });
  await page.goto("/");
  const more = page.getByRole("button", { name: "More", exact: true });
  await more.click();
  await page.getByRole("menuitem", { name: "Export queue summary…" }).click();
  await expect.poll(() => calls).toEqual(["summary"]);
  const openBackup = async () => {
    await more.click();
    await page.getByRole("menuitem", { name: "Back up queue with source URLs…" }).click();
  };
  await openBackup();
  const dialog = page.getByRole("dialog", { name: "Back up queue with source URLs?" });
  await expect(dialog.getByText(/plain-text source URLs/)).toBeVisible();
  await expect(dialog.getByRole("button", { name: "Cancel" })).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(dialog).toBeHidden();
  expect(calls).toEqual(["summary"]);
  await openBackup();
  await dialog.getByRole("button", { name: "Save backup" }).click();
  await expect.poll(() => calls).toEqual(["summary", "backup"]);
});

test("requires an explicit choice before importing old queue links", async ({ page }) => {
  const choices: unknown[][] = [];
  await mockDesktop(page, {
    ImportQueue: () => ({ added: 0, failed: [], legacyId: "pending" }),
    FinishLegacyImport: (...args) => {
      choices.push(args);
      return { added: args[1] ? 2 : 0, failed: [] };
    },
  });
  await page.goto("/");
  const openImport = async () => {
    await page.getByRole("button", { name: "More", exact: true }).click();
    await page.getByRole("menuitem", { name: /Import queue/ }).click();
  };
  await openImport();
  const dialog = page.getByRole("dialog", { name: "Import an older queue file?" });
  await expect(dialog.getByText(/incomplete links/)).toBeVisible();
  await expect(dialog.getByRole("button", { name: "Cancel" })).toBeFocused();
  expect(choices).toEqual([]);
  await page.keyboard.press("Escape");
  await expect.poll(() => choices).toEqual([["pending", false]]);
  await openImport();
  await dialog.getByRole("button", { name: "Import these links" }).click();
  await expect.poll(() => choices).toEqual([["pending", false], ["pending", true]]);
  await expect(page.getByText("Added 2 downloads")).toBeVisible();
});
