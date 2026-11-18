import { expect, test } from "@playwright/test";
import { mockDesktop } from "./desktop";

function task(id: string, status: string, recovery?: string) {
  return {
    id,
    url: "https://example.test/" + id + ".zip",
    destination: "C:\\Downloads\\" + id + ".zip",
    status,
    recovery,
    completed: 0,
    total: 1024,
    priority: 0,
    createdAt: "2026-11-16T08:00:00Z",
    updatedAt: "2026-11-16T08:00:00Z",
  };
}

test("selects tasks by keyboard and reports applied, failed, and ineligible outcomes", async ({ page }) => {
  const items = [
    task("queued", "queued"),
    task("running", "running"),
    task("paused", "paused"),
    task("failed", "failed"),
    task("recovery", "failed", "restart-required"),
    task("finished", "completed"),
  ];
  await mockDesktop(page, {
    List: () => items,
    ApplyTaskAction: (action, ids) => (ids as string[]).map((id) => {
      const item = items.find((row) => row.id === id)!;
      if (id === "running") return { id, error: "Download already finished" };
      if (action === "pause" && item.status === "queued") item.status = "paused";
      else if (action === "resume" && item.status === "paused") item.status = "queued";
      else if (action === "retry" && item.status === "failed" && !item.recovery) item.status = "queued";
      else throw new Error("Unexpected action for " + id);
      return { id };
    }),
  });
  await page.goto("/");
  const queued = page.getByRole("checkbox", { name: "Select queued.zip", exact: true });
  await queued.press("Space");
  await expect(queued).toBeChecked();
  const selectAll = page.getByRole("checkbox", { name: "Select all visible tasks" });
  await expect(selectAll).toHaveJSProperty("indeterminate", true);
  await selectAll.press("Space");
  await expect(selectAll).toBeChecked();
  await expect(page.getByText("6 selected")).toBeVisible();

  const toolbar = page.getByRole("group", { name: "Selected tasks" });
  await toolbar.getByRole("button", { name: "Pause (2 of 6)" }).press("Enter");
  const outcomes = page.getByRole("region", { name: "Bulk action results" });
  await expect(outcomes).toContainText("1 succeeded, 1 failed, 4 skipped");
  await expect(outcomes.getByRole("listitem").filter({ hasText: "queued.zip" })).toContainText("Succeeded");
  await expect(outcomes.getByRole("listitem").filter({ hasText: "running.zip" })).toContainText("Download already finished");
  await expect(outcomes.getByRole("listitem").filter({ hasText: "finished.zip" })).toContainText("Skipped");
  await expect(toolbar.getByRole("button", { name: "Resume (2 of 6)" })).toBeEnabled();
  await toolbar.getByRole("button", { name: "Resume (2 of 6)" }).press("Enter");
  await expect(outcomes).toContainText("2 succeeded, 0 failed, 4 skipped");
  await toolbar.getByRole("button", { name: "Retry (1 of 6)" }).press("Enter");
  await expect(outcomes.getByRole("listitem").filter({ hasText: "recovery.zip" })).toContainText("Skipped");
  await expect(toolbar.getByRole("button", { name: "Retry (0 of 6)" })).toBeDisabled();
});

test("scopes selection to the current filters and drops rows that leave the view", async ({ page }) => {
  const paused = task("paused", "paused");
  await mockDesktop(page, {
    List: () => [paused, task("queued", "queued")],
    ApplyTaskAction: (action, ids) => {
      expect(action).toBe("resume");
      expect(ids).toEqual([paused.id]);
      paused.status = "queued";
      return [{ id: paused.id }];
    },
  });
  await page.goto("/");
  const selectAll = page.getByRole("checkbox", { name: "Select all visible tasks" });
  await selectAll.check();
  await page.getByRole("textbox").fill("paused.zip");
  await expect(selectAll).not.toBeChecked();
  await selectAll.check();
  await expect(page.getByText("1 selected")).toBeVisible();
  await page.getByRole("textbox").clear();
  await expect(selectAll).not.toBeChecked();
  await selectAll.check();
  await page.getByRole("button", { name: /^Paused/ }).click();
  await expect(selectAll).not.toBeChecked();
  await selectAll.check();
  await page.getByRole("button", { name: "Resume (1 of 1)" }).click();
  await expect(page.getByRole("region", { name: "Bulk action results" })).toContainText("1 succeeded, 0 failed, 0 skipped");
  await expect(page.getByRole("checkbox", { name: "Select paused.zip" })).toHaveCount(0);
  await page.getByRole("button", { name: /^All Downloads/i }).click();
  await expect(page.getByRole("checkbox", { name: "Select paused.zip" })).not.toBeChecked();
});

test("requires an explicit record-only or file deletion choice and refreshes partially deleted tasks", async ({ page }) => {
  await page.clock.install();
  const items = [
    task("record", "completed"),
    task("removed", "completed"),
    task("locked", "completed"),
    task("busy", "running"),
  ];
  const actions: unknown[][] = [];
  await mockDesktop(page, {
    List: () => items,
    ApplyTaskAction: (action, ids) => {
      actions.push([action, ids]);
      return (ids as string[]).map((id) => {
        items.splice(items.findIndex((item) => item.id === id), 1);
        return id === "locked" ? {
          id,
          error: "Download deleted, but removing its files failed: File is in use",
          fileCleanupError: "File is in use",
        } : { id };
      });
    },
  });
  await page.goto("/");
  await page.getByRole("checkbox", { name: "Select record.zip" }).check();
  await page.getByRole("button", { name: "Remove (1 of 1)" }).press("Enter");
  const dialog = page.getByRole("dialog", { name: "Remove selected downloads?" });
  await expect(dialog.getByRole("button", { name: "Cancel" })).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(dialog).toHaveCount(0);
  expect(actions).toEqual([]);

  await page.getByRole("button", { name: "Remove (1 of 1)" }).click();
  await dialog.getByRole("button", { name: "Remove records only" }).click();
  await expect(page.getByRole("checkbox", { name: "Select record.zip" })).toHaveCount(0);
  const notice = page.getByRole("region", { name: "Bulk deletion result" });
  await expect(notice).toContainText("Local files were kept");
  expect(actions).toEqual([["remove-records", ["record"]]]);

  await page.getByRole("checkbox", { name: "Select all visible tasks" }).check();
  await page.getByRole("button", { name: "Remove (2 of 3)" }).click();
  await expect(dialog).toContainText("2 of 3 selected tasks");
  await dialog.getByRole("button", { name: "Remove records and delete files" }).click();
  await expect(notice).toContainText("1 succeeded · 1 failed · 1 skipped");
  await expect(notice).not.toContainText("locked.zip");
  await page.clock.fastForward(10_000);
  const detailsButton = notice.getByRole("button", { name: "Details" });
  await detailsButton.click();
  const outcomes = page.getByRole("dialog", { name: "Bulk deletion results" });
  const locked = outcomes.getByRole("listitem").filter({ hasText: "locked.zip" });
  await expect(locked).toContainText("File cleanup failed");
  await expect(locked).toContainText("Download record removed. File is in use");
  await expect(outcomes.getByRole("listitem").filter({ hasText: "busy.zip" })).toContainText("Skipped");
  await expect(outcomes.getByText("removed.zip", { exact: true })).toBeHidden();
  await outcomes.getByText("Show 1 successful item").click();
  await expect(outcomes.getByText("removed.zip", { exact: true })).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(detailsButton).toBeFocused();
  await notice.getByRole("button", { name: "Dismiss bulk action results" }).press("Enter");
  await expect(notice).toBeHidden();
  await expect(page.getByRole("textbox")).toBeFocused();
  await expect(page.getByRole("checkbox", { name: "Select locked.zip" })).toHaveCount(0);
  await expect(page.getByRole("checkbox", { name: "Select busy.zip" })).toBeChecked();
  expect(actions).toHaveLength(2);
  expect(actions[1][0]).toBe("remove-files");
  expect(actions[1][1]).toEqual(expect.arrayContaining(["locked", "removed"]));
  expect(actions[1][1]).toHaveLength(2);
});

test("keeps removal scoped to tasks still visible and eligible after confirmation opens", async ({ page }) => {
  const finished = task("finished-during-confirmation", "queued");
  const waiting = task("waiting", "queued");
  const running = task("running", "running");
  const items = [finished, waiting, running];
  const actions: unknown[][] = [];
  await mockDesktop(page, {
    List: () => items,
    ApplyTaskAction: (action, ids) => {
      actions.push([action, ids]);
      return (ids as string[]).map((id) => ({ id }));
    },
  });
  await page.goto("/");
  await page.getByRole("button", { name: /^Active/ }).click();
  await page.getByRole("checkbox", { name: "Select all visible tasks" }).check();
  await page.getByRole("button", { name: "Remove (2 of 3)" }).click();
  const dialog = page.getByRole("dialog", { name: "Remove selected downloads?" });

  finished.status = "completed";
  running.status = "queued";
  await page.evaluate((updated) => {
    const { _wails } = window as typeof window & { _wails: { dispatchWailsEvent(event: unknown): void } };
    for (const item of updated) {
      _wails.dispatchWailsEvent({ name: "download", data: { type: "updated", item, sequence: 1 } });
    }
  }, [finished, running]);

  await expect(dialog).toContainText("1 of 3 selected tasks");
  await dialog.getByRole("button", { name: "Remove records and delete files" }).click();
  await expect(page.getByRole("region", { name: "Bulk deletion result" })).toContainText("1 succeeded · 2 skipped");
  expect(actions).toEqual([["remove-files", [waiting.id]]]);
});

test("shows successful deletion as a timed notification with details still available from the menu", async ({ page }) => {
  await page.clock.install();
  let items = [task("finished", "completed")];
  await mockDesktop(page, {
    List: () => items,
    ApplyTaskAction: () => {
      items = [];
      return [{ id: "finished" }];
    },
  });
  await page.goto("/");
  await page.getByRole("checkbox", { name: "Select all visible tasks" }).check();
  await page.getByRole("button", { name: "Remove (1 of 1)" }).click();
  await page.getByRole("button", { name: "Remove records and delete files" }).click();

  const notice = page.getByRole("region", { name: "Bulk deletion result" });
  await expect(notice).toContainText("Removed 1 download record");
  await expect(notice).toContainText("File cleanup complete");
  await expect(notice).not.toContainText("finished.zip");
  await notice.hover();
  await page.clock.fastForward(10_000);
  await expect(notice).toBeVisible();
  await notice.getByRole("button", { name: "Details" }).focus();
  await page.mouse.move(0, 0);
  await page.clock.fastForward(10_000);
  await expect(notice).toBeVisible();
  await page.getByRole("textbox").focus();
  await page.clock.fastForward(8_001);
  await expect(notice).toBeHidden();

  await page.getByRole("button", { name: "More", exact: true }).click();
  await page.getByRole("menuitem", { name: "Latest deletion result" }).click();
  await expect(page.getByRole("dialog", { name: "Bulk deletion results" })).toContainText("finished.zip");
});

test("keeps unconfirmed deletion outcomes available after a request error", async ({ page }) => {
  await page.clock.install();
  await mockDesktop(page, {
    List: () => [task("finished", "completed")],
    ApplyTaskAction: () => { throw new Error("Response unavailable"); },
  });
  await page.goto("/");
  await page.getByRole("checkbox", { name: "Select all visible tasks" }).check();
  await page.getByRole("button", { name: "Remove (1 of 1)" }).click();
  await page.getByRole("button", { name: "Remove records and delete files" }).click();

  const notice = page.getByRole("region", { name: "Bulk deletion result" });
  await expect(notice).toContainText("1 unconfirmed");
  await page.clock.fastForward(10_000);
  await notice.getByRole("button", { name: "Details" }).click();
  const outcomes = page.getByRole("dialog", { name: "Bulk deletion results" });
  await expect(outcomes).toContainText("Response unavailable");
  await expect(outcomes.getByRole("listitem").filter({ hasText: "finished.zip" })).toContainText("Unconfirmed");
});
