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
  expect(updates).toEqual([[task.id, { priority: 1, connections: 0, speedLimit: 512000 }]]);
});
