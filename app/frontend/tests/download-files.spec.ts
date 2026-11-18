import { expect, test } from "@playwright/test";
import { mockDesktop } from "./desktop";

test("checks completed files on load, on focus, and while browsing", async ({ page }) => {
  await page.clock.install();
  const completed = {
    id: "completed", status: "completed", destination: "/downloads/video.ts",
    url: "https://example.test/video.ts", completed: 1024, total: 1024,
    createdAt: "2026-11-18T01:00:00Z", updatedAt: "2026-11-18T01:00:00Z",
  };
  const queued = { ...completed, id: "queued", status: "queued", destination: "/downloads/next.ts", completed: 0 };
  let file: "missing" | "folder-missing" | "present" = "missing";
  const checks: unknown[] = [];
  let opens = 0;
  let reveals = 0;
  await mockDesktop(page, {
    CanConvert: () => true,
    List: () => [completed, queued],
    MissingDownloadFiles: (ids) => {
      checks.push(ids);
      return file === "present" ? {} : { completed: { destination: completed.destination, folderMissing: file === "folder-missing" } };
    },
    Open: () => { opens++; },
    Reveal: () => { reveals++; },
  });
  await page.goto("/");
  const row = page.getByRole("listitem").filter({ hasText: "video.ts" });
  const open = row.getByRole("button", { name: "Open for video.ts" });
  const reveal = row.getByRole("button", { name: "Show in folder for video.ts" });
  await expect(row.getByText("File missing", { exact: true })).toBeVisible();
  expect(checks[0]).toEqual([completed.id]);
  await expect(row.getByText("Completed", { exact: true })).toBeVisible();
  await expect(open).toHaveCount(0);
  await expect(row.getByRole("button", { name: "Convert to MP4 for video.ts" })).toHaveCount(0);
  await row.press("Enter");
  await expect(page.locator(".toast")).toHaveText("File missing");
  expect(opens).toBe(0);
  await reveal.click();
  await expect.poll(() => reveals).toBe(1);

  await row.getByRole("button", { name: "Details for video.ts" }).click();
  const details = page.getByRole("dialog", { name: "Download details" });
  await expect(details.getByText("File missing", { exact: true })).toBeVisible();
  file = "folder-missing";
  await page.evaluate(() => window.dispatchEvent(new Event("focus")));
  await expect(details.getByText("Download folder missing", { exact: true })).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(reveal).toHaveCount(0);

  file = "present";
  await page.clock.fastForward(5000);
  await expect(row.getByText("Download folder missing", { exact: true })).toHaveCount(0);
  await open.click();
  await expect.poll(() => opens).toBe(1);
});
