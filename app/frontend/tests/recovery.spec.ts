import { expect, test } from "@playwright/test";
import { mockDesktop } from "./desktop";

test("refreshes an existing link and offers an explicit restart", async ({ page }) => {
  const id = "signed-task";
  const fresh = "https://example.test/file.bin?signature=fresh";
  const item = {
    id,
    url: "https://example.test/file.bin?signature=expired",
    destination: "C:\\Downloads\\file.bin",
    status: "failed",
    error: "probe: HTTP 403 Forbidden",
    recovery: "",
    completed: 512,
    total: 1024,
    priority: 0,
    createdAt: "2026-11-16T07:00:00Z",
    updatedAt: "2026-11-16T07:00:00Z",
  };
  const refreshes: unknown[][] = [];
  const restarts: unknown[][] = [];
  await mockDesktop(page, {
    List: () => [item],
    Reauthenticate: (...args) => {
      refreshes.push(args);
      item.url = fresh;
      item.recovery = "restart-required";
      return null;
    },
    Restart: (...args) => {
      restarts.push(args);
      return null;
    },
  });

  await page.goto("/");
  const rowName = page.getByText("file.bin", { exact: true });
  await rowName.click({ button: "right" });
  await page.getByRole("menuitem", { name: "Refresh download link" }).click();
  const dialog = page.getByRole("dialog");
  await dialog.getByRole("textbox", { name: "Fresh URL or cURL command" }).fill(fresh);
  await dialog.getByRole("button", { name: "Use fresh link" }).click();
  await expect(dialog).toHaveCount(0);
  expect(refreshes).toEqual([[id, fresh]]);

  await page.reload();
  await expect(page.getByText("The fresh link could not be matched to the saved data.")).toBeVisible();
  await rowName.click({ button: "right" });
  await expect(page.getByRole("menuitem", { name: "Retry", exact: true })).toHaveCount(0);
  await page.getByRole("menuitem", { name: "Recover download" }).click();
  await expect(dialog.getByText(/discards the partial download/)).toBeVisible();
  expect(restarts).toEqual([]);
  await dialog.getByRole("button", { name: "Restart from beginning" }).click();
  await expect(dialog).toHaveCount(0);
  expect(restarts).toEqual([[id]]);
});
