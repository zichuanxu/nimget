import { expect, test } from "@playwright/test";
import { mockDesktop } from "./desktop";

const task = {
  id: "report",
  url: "https://test-user:test-password@example.test/report.zip?signature=test-secret",
  destination: "C:\\Downloads\\report.zip",
  status: "running",
  completed: 512,
  total: 1024,
  priority: 0,
  connections: 4,
  speedLimit: 1048576,
  createdAt: "2026-11-16T08:00:00Z",
  updatedAt: "2026-11-16T08:00:00Z",
};

test("inspects a running download, validates edits, and saves task settings", async ({ page }) => {
  const updates: unknown[][] = [];
  await mockDesktop(page, {
    List: () => [task],
    Update: (...args) => {
      updates.push(args);
      return null;
    },
  });
  await page.goto("/");
  const details = page.getByRole("button", { name: "Details for report.zip" });
  await details.focus();
  await details.press("Enter");
  const dialog = page.getByRole("dialog", { name: "Download details" });
  await expect(dialog.getByRole("combobox", { name: "Priority" })).toBeFocused();
  await expect(dialog.getByText(task.destination, { exact: true })).toBeVisible();
  await expect(dialog.getByText("Downloading", { exact: true })).toBeVisible();
  await expect(dialog.getByText("512 B of 1.00 KiB")).toBeVisible();
  await expect(dialog).not.toContainText(/test-password|test-secret/);

  const priority = dialog.getByLabel("Priority", { exact: true });
  const connections = dialog.getByLabel("Connections", { exact: true });
  const speedLimit = dialog.getByLabel("Speed limit", { exact: true });
  await expect(connections).toHaveValue("4");
  await expect(speedLimit).toHaveValue("1M");
  await expect(dialog.getByText(/queued download starts next/)).toBeVisible();
  await expect(dialog.getByText(/next attempt/)).toBeVisible();
  await expect(dialog.getByText(/running download/)).toBeVisible();

  await connections.fill("33");
  await dialog.getByRole("button", { name: "Save" }).click();
  await expect(dialog.getByRole("alert")).toContainText("1 to 32");
  expect(updates).toEqual([]);
  await connections.fill("");
  await speedLimit.fill("too fast");
  await dialog.getByRole("button", { name: "Save" }).click();
  await expect(dialog.getByRole("alert")).toContainText("500K");
  expect(updates).toEqual([]);

  await priority.selectOption("1");
  await speedLimit.fill("500K");
  await dialog.getByRole("button", { name: "Save" }).click();
  await expect(dialog).toHaveCount(0);
  await expect(details).toBeFocused();
  expect(updates).toEqual([[task.id, { priority: 1, connections: 0, speedLimit: 512000 }]]);
});

for (const [failure, guidance] of [
  ["disk-space", /Free space on the destination drive/],
  ["filesystem-permission", /Make sure NimGet can write to the destination folder/],
  ["network", /Check your connection and proxy settings/],
] as const) {
  test(`explains ${failure} and retries the existing task`, async ({ page }) => {
    const retries: unknown[][] = [];
    await mockDesktop(page, {
      List: () => [{ ...task, status: "failed", failure, error: "server message with test-secret" }],
      Retry: (...args) => retries.push(args),
    });
    await page.goto("/");
    await page.getByText("report.zip", { exact: true }).hover();
    await page.getByRole("button", { name: "Details for report.zip" }).click();
    const details = page.getByRole("dialog", { name: "Download details" });
    await expect(details.getByText(guidance)).toBeVisible();
    await expect(details).not.toContainText("test-secret");
    await details.getByRole("button", { name: "Retry" }).click();
    await expect(details).toHaveCount(0);
    expect(retries).toEqual([[task.id]]);
  });
}

test("opens explicit link recovery when the existing bytes need a restart decision", async ({ page }) => {
  await mockDesktop(page, {
    List: () => [{ ...task, status: "failed", failure: "authorization", recovery: "restart-required" }],
  });
  await page.goto("/");
  await page.getByText("report.zip", { exact: true }).click({ button: "right" });
  await page.getByRole("menuitem", { name: "Download details" }).click();
  const details = page.getByRole("dialog", { name: "Download details" });
  await expect(details.getByText(/fresh link could not be matched/)).toBeVisible();
  await expect(details.getByRole("button", { name: "Retry", exact: true })).toHaveCount(0);
  await details.getByRole("button", { name: "Recover download" }).click();
  const recovery = page.getByRole("dialog", { name: "Recover download" });
  await expect(recovery.getByText(/discards the partial download/)).toBeVisible();
  await expect(recovery.getByRole("textbox", { name: "Fresh URL or cURL command" })).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(recovery).toHaveCount(0);
  await expect(page.getByRole("listitem")).toBeFocused();
});

test("offers a fresh link after the website rejects authorization", async ({ page }) => {
  await mockDesktop(page, {
    List: () => [{ ...task, status: "failed", failure: "authorization" }],
  });
  await page.goto("/");
  await page.getByText("report.zip", { exact: true }).hover();
  await page.getByRole("button", { name: "Details for report.zip" }).click();
  const details = page.getByRole("dialog", { name: "Download details" });
  await expect(details.getByText(/Sign in on the website/)).toBeVisible();
  await details.getByRole("button", { name: "Refresh download link" }).click();
  await expect(page.getByRole("dialog", { name: "Recover download" })).toBeVisible();
});
