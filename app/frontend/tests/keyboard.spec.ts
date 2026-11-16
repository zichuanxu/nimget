import { expect, test } from "@playwright/test";
import { mockDesktop } from "./desktop";

const task = {
  id: "report",
  url: "https://example.test/report.zip",
  destination: "C:\\Downloads\\report.zip",
  status: "running",
  completed: 512,
  total: 1024,
  priority: 0,
  createdAt: "2026-11-16T08:00:00Z",
  updatedAt: "2026-11-16T08:00:00Z",
};

test("keeps keyboard focus within a named add dialog and restores its trigger", async ({ page }) => {
  await mockDesktop(page);
  await page.goto("/");
  const trigger = page.getByRole("button", { name: "New download", exact: true }).first();
  await page.keyboard.press("Tab");
  await expect(trigger).toBeFocused();
  await page.keyboard.press("Enter");

  const dialog = page.getByRole("dialog", { name: "New download" });
  const links = dialog.getByRole("textbox", { name: /^Links/ });
  await expect(links).toBeFocused();
  await trigger.evaluate((element) => element.focus());
  await expect(links).toBeFocused();
  await expect(dialog).toMatchAriaSnapshot(`
    - dialog "New download":
      - banner:
        - heading "New download" [level=2]
  `);
  for (const key of ["Tab", "Shift+Tab"]) {
    for (let i = 0; i < 10; i++) {
      await page.keyboard.press(key);
      expect(await dialog.evaluate((element) => {
        const active = document.activeElement;
        return active === document.body || element.contains(active);
      })).toBe(true);
    }
  }
  await page.keyboard.press("Escape");
  await expect(dialog).toBeHidden();
  await expect(trigger).toBeFocused();
  await page.keyboard.press("Enter");
  await expect(dialog).toBeVisible();
  await page.mouse.click(4, 4);
  await expect(dialog).toBeHidden();
  await expect(trigger).toBeFocused();
});

test("selects a task and opens its action menu and delete confirmation by keyboard", async ({ page }) => {
  const calls: string[] = [];
  await mockDesktop(page, {
    List: () => [task],
    Pause: () => { calls.push("pause"); },
    Reveal: () => { calls.push("reveal"); },
    Delete: () => { calls.push("delete"); },
  });
  await page.goto("/");

  const more = page.getByRole("button", { name: "More actions for report.zip" });
  await more.focus();
  await page.keyboard.press("Space");
  const menu = page.getByRole("menu", { name: "More actions for report.zip" });
  await expect(more).toHaveAttribute("aria-expanded", "true");
  await expect(menu.getByRole("menuitem", { name: "Download details" })).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(menu).toBeHidden();
  await expect(more).toBeFocused();
  await page.keyboard.press("Enter");
  await page.keyboard.press("Shift+Tab");
  await expect(menu).toBeHidden();
  await expect(page.getByRole("button", { name: "Details for report.zip" })).toBeFocused();
  await page.keyboard.press("Tab");
  await expect(more).toBeFocused();
  await page.keyboard.press("Enter");
  await page.keyboard.press("End");
  const remove = menu.getByRole("menuitem", { name: "Delete" });
  await expect(remove).toBeFocused();
  await page.keyboard.press("Enter");

  const dialog = page.getByRole("dialog", { name: "Delete this download?" });
  await expect(dialog.getByRole("button", { name: "Cancel" })).toBeFocused();
  await page.keyboard.press("ArrowDown");
  await page.keyboard.press("Delete");
  await page.keyboard.press("Control+n");
  await expect(dialog).toBeVisible();
  await page.keyboard.press("Enter");
  await expect(dialog).toBeHidden();
  await expect(more).toBeFocused();
  expect(calls).toEqual([]);
});

test("returns to New download when the clipboard download trigger is consumed", async ({ page }) => {
  await mockDesktop(page);
  await page.goto("/");
  const fallback = page.getByRole("button", { name: "New download", exact: true }).first();
  await expect(fallback).toBeVisible();
  await page.evaluate(() => {
    const { _wails } = window as typeof window & { _wails: { dispatchWailsEvent(event: unknown): void } };
    _wails.dispatchWailsEvent({ name: "clipboard", data: "https://example.test/report.zip" });
  });
  const clipboard = page.getByRole("button", { name: "Download", exact: true });
  await clipboard.focus();
  await page.keyboard.press("Enter");
  await expect(page.getByRole("dialog", { name: "New download" })).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(fallback).toBeFocused();
});

test("exposes task selection and actions without intercepting keys in search", async ({ page }) => {
  const requests: string[] = [];
  await mockDesktop(page, {
    List: () => [task],
    Pause: (id) => { requests.push(`pause:${id}`); },
    Reveal: (id) => { requests.push(`show:${id}`); },
  });
  await page.goto("/");
  const row = page.getByRole("list", { name: "All downloads" }).getByRole("listitem");
  await expect(row).toContainText("Downloading");
  await page.keyboard.press("ArrowDown");
  await expect(row).toBeFocused();
  await expect(row).toHaveAttribute("aria-current", "true");

  await page.keyboard.press("Control+f");
  const search = page.getByRole("textbox", { name: "Search downloads" });
  await expect(search).toBeFocused();
  await page.keyboard.press("Enter");
  await page.keyboard.press("Delete");
  await page.keyboard.press("Space");
  await expect(search).toHaveValue(" ");
  await expect(page.getByRole("dialog")).toHaveCount(0);
  expect(requests).toEqual([]);

  const select = row.getByRole("checkbox", { name: "Select report.zip" });
  await select.focus();
  await page.keyboard.press("Space");
  await expect(select).toBeChecked();
  await page.keyboard.press("Space");
  await expect(select).not.toBeChecked();
  const pause = row.getByRole("button", { name: "Pause for report.zip" });
  await page.keyboard.press("Tab");
  await expect(pause).toBeFocused();
  await page.keyboard.press("Space");
  await expect.poll(() => requests).toEqual(["pause:report"]);
});
