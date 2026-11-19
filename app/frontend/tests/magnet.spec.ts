import { expect, test } from "@playwright/test";
import { mockDesktop } from "./desktop";

test("queues a magnet from New Download using the chosen save directory", async ({ page }) => {
  const link = "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&tr=https%3A%2F%2Fexample.test%2Fannounce";
  let request: unknown;
  await mockDesktop(page, {
    PickDirectory: () => "C:\\Downloads\\Torrents",
    Add: (input) => {
      request = input;
      return { id: "magnet-task" };
    },
  });

  await page.goto("/");
  await page.getByRole("button", { name: "New download" }).first().click();
  const dialog = page.getByRole("dialog");
  await dialog.getByRole("textbox", { name: /^Links/ }).fill(link);
  await dialog.getByRole("button", { name: "Choose…" }).click();
  await dialog.getByRole("button", { name: "Download", exact: true }).click({ timeout: 2500 });
  await expect(dialog).toHaveCount(0);
  expect(request).toMatchObject({ url: link, directory: "C:\\Downloads\\Torrents" });
  expect(request).not.toHaveProperty("destination");
});

test("shows distinct magnet phases and progress after metadata is ready", async ({ page }) => {
  const stamp = "2026-11-18T20:00:00Z";
  const base = {
    url: "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567",
    destination: "",
    priority: 0,
    status: "running",
    completed: 0,
    total: -1,
    sourceKind: "magnet",
    createdAt: stamp,
    updatedAt: stamp,
  };
  await mockDesktop(page, {
    List: () => [
      { ...base, id: "metadata", name: "Provisional name", phase: "metadata" },
      { ...base, id: "waiting", name: "Waiting.mp4", phase: "waiting-peers", total: 4096 },
      { ...base, id: "transfer", name: "Transfer.txt", phase: "transferring", completed: 2048, total: 4096 },
    ],
  });

  await page.goto("/");
  const metadata = page.getByRole("listitem").filter({ hasText: "Provisional name" });
  await expect(metadata.getByText("Retrieving file list")).toBeVisible();
  await expect(metadata.getByRole("progressbar")).not.toHaveAttribute("value");
  await expect(page.getByRole("listitem").filter({ hasText: "Waiting.mp4" }).getByText("Waiting for peers")).toBeVisible();
  await expect(page.getByRole("listitem").filter({ hasText: "Transfer.txt" }).getByRole("progressbar")).toHaveAttribute("value", "50");
});

test("retries a verified magnet with a publication problem from Task Details", async ({ page }) => {
  const retries: unknown[] = [];
  await mockDesktop(page, {
    List: () => [{
      id: "recoverable-output",
      name: "publication.bin",
      url: "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567",
      sourceKind: "magnet",
      destination: "C:\\Downloads\\publication.bin",
      status: "failed",
      error: "Could not publish the verified magnet output. Retry after checking the save folder.",
      completed: 4096,
      total: 4096,
      createdAt: "2026-11-18T20:00:00Z",
      updatedAt: "2026-11-18T20:00:00Z",
    }],
    Retry: (id) => { retries.push(id); },
  });
  await page.goto("/");
  await page.getByText("publication.bin", { exact: true }).hover();
  await page.getByRole("button", { name: "Details for publication.bin" }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog.getByText(/Could not publish the verified magnet output/)).toBeVisible();
  await dialog.getByRole("button", { name: "Retry", exact: true }).click();
  await expect(dialog).toHaveCount(0);
  expect(retries).toEqual(["recoverable-output"]);
});
