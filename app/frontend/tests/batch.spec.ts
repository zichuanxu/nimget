import { expect, test } from "@playwright/test";
import type { Request } from "../bindings/github.com/amemiya02/nimget/internal/download";
import { mockDesktop } from "./desktop";

const firstURL = "https://example.test/first.zip";
const rejectedURL = "invalid URL";
const lastURL = "https://example.test/last.zip";

test("reports each accepted and rejected entry in a mixed batch", async ({ page }) => {
  await mockDesktop(page, {
    Add: (input) => {
      const { url } = input as Request;
      if (url === rejectedURL) throw new Error("Download URL must be an absolute HTTP or HTTPS URL");
      return { id: url === firstURL ? "task-first" : "task-last", url };
    },
  });

  await page.goto("/");
  await page.getByRole("button", { name: "New download" }).first().click();
  const dialog = page.getByRole("dialog");
  await dialog.getByRole("textbox", { name: /^Links/ }).fill([firstURL, rejectedURL, lastURL].join("\n"));
  await dialog.getByRole("button", { name: "Add 3 downloads" }).click({ timeout: 2500 });

  await expect(dialog.getByRole("status")).toHaveText("2 accepted, 1 rejected");
  const entries = dialog.getByRole("listitem");
  await expect(entries).toHaveCount(3);
  await expect(entries.nth(0)).toContainText("Accepted");
  await expect(entries.nth(0)).toContainText(firstURL);
  await expect(entries.nth(0)).toContainText("task-first");
  await expect(entries.nth(1)).toContainText("Rejected");
  await expect(entries.nth(1)).toContainText("Download URL must be an absolute HTTP or HTTPS URL");
  await expect(entries.nth(2)).toContainText("Accepted");
  await expect(entries.nth(2)).toContainText(lastURL);
  await expect(entries.nth(2)).toContainText("task-last");
});

test("reopens a partial batch and retries only rejected entries", async ({ page }) => {
  const correctedURL = "https://example.test/corrected.zip";
  const submissions: string[] = [];
  let correctedAttempts = 0;
  await mockDesktop(page, {
    Add: (input) => {
      const { url } = input as Request;
      submissions.push(url);
      if (url === rejectedURL) throw new Error("Invalid download URL");
      if (url === correctedURL && ++correctedAttempts === 1) throw new Error("Download service temporarily unavailable");
      return { id: url === firstURL ? "task-first" : "task-corrected", url };
    },
  });

  await page.goto("/");
  const newDownload = page.getByRole("button", { name: "New download" }).first();
  await newDownload.click();
  const dialog = page.getByRole("dialog");
  await dialog.getByRole("textbox", { name: /^Links/ }).fill([firstURL, rejectedURL].join("\n"));
  await dialog.getByRole("button", { name: "Add 2 downloads" }).click();
  await expect(dialog.getByRole("status")).toHaveText("1 accepted, 1 rejected");

  await dialog.getByRole("button", { name: "Close", exact: true }).last().click();
  await expect(dialog).toHaveCount(0);
  await newDownload.click();

  await expect(dialog.getByRole("status")).toHaveText("1 accepted, 1 rejected", { timeout: 2500 });
  await expect(dialog.getByText("Task ID task-first")).toBeVisible();
  await dialog.getByRole("textbox", { name: "Rejected link 2" }).fill(correctedURL);
  await dialog.getByRole("button", { name: "Retry rejected links" }).click();
  await expect(dialog.getByText("Download service temporarily unavailable")).toBeVisible();
  await expect(dialog.getByText("Task ID task-first")).toBeVisible();

  await dialog.getByRole("button", { name: "Retry rejected links" }).click();
  await expect(dialog.getByRole("status")).toHaveText("2 accepted, 0 rejected");
  await expect(dialog.getByText("Task ID task-first")).toBeVisible();
  await expect(dialog.getByText("Task ID task-corrected")).toBeVisible();
  expect(submissions).toEqual([firstURL, rejectedURL, correctedURL, correctedURL]);
});

test("accepts duplicate URLs when entered intentionally in a batch or in a new download", async ({ page }) => {
  const submissions: string[] = [];
  await mockDesktop(page, {
    ExpandBatch: () => [firstURL, rejectedURL, firstURL],
    Add: (input) => {
      const { url } = input as Request;
      submissions.push(url);
      if (url === rejectedURL) throw new Error("Invalid download URL");
      return { id: `task-${submissions.length}`, url };
    },
  });

  await page.goto("/");
  await page.getByRole("button", { name: "New download" }).first().click();
  const dialog = page.getByRole("dialog");
  await dialog.getByRole("textbox", { name: /^Links/ }).fill("https://example.test/{first,rejected,first}.zip");
  await dialog.getByRole("button", { name: "Add 3 downloads" }).click();
  await expect(dialog.getByRole("status")).toHaveText("2 accepted, 1 rejected");
  await expect(dialog.getByText("Task ID task-1")).toBeVisible();
  await expect(dialog.getByText("Task ID task-3")).toBeVisible();

  await dialog.getByRole("button", { name: "Start new download" }).click();
  const links = dialog.getByRole("textbox", { name: /^Links/ });
  await expect(links).toBeEmpty();
  await links.fill(firstURL);
  await dialog.getByRole("button", { name: "Download", exact: true }).click();
  await expect(dialog).toHaveCount(0);
  expect(submissions).toEqual([firstURL, rejectedURL, firstURL, firstURL]);
});

test("offers a dropped link as a new download when a partial batch is retained", async ({ page }) => {
  await mockDesktop(page, {
    Add: (input) => {
      const { url } = input as Request;
      if (url === rejectedURL) throw new Error("Invalid download URL");
      return { id: "task-first", url };
    },
  });

  await page.goto("/");
  await page.getByRole("button", { name: "New download" }).first().click();
  const dialog = page.getByRole("dialog");
  await dialog.getByRole("textbox", { name: /^Links/ }).fill([firstURL, rejectedURL].join("\n"));
  await dialog.getByRole("button", { name: "Add 2 downloads" }).click();
  await expect(dialog.getByRole("status")).toHaveText("1 accepted, 1 rejected");
  await dialog.getByRole("button", { name: "Close", exact: true }).last().click();

  const dataTransfer = await page.evaluateHandle((url) => {
    const transfer = new DataTransfer();
    transfer.setData("text/uri-list", url);
    return transfer;
  }, lastURL);
  await page.locator(".shell").dispatchEvent("drop", { dataTransfer });

  await expect(dialog.getByRole("status")).toHaveText("1 accepted, 1 rejected");
  await dialog.getByRole("button", { name: "Start new download with supplied link" }).click();
  await expect(dialog.getByRole("textbox", { name: /^Links/ })).toHaveValue(lastURL);
});
