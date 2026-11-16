import { expect, test, type Page } from "@playwright/test";
import { mockDesktop } from "./desktop";

function task(index: number, status = "completed") {
  const id = "task-" + String(index).padStart(4, "0");
  const createdAt = new Date(Date.UTC(2026, 10, 16, 8) - index * 1000).toISOString();
  return {
    id, status,
    url: "https://example.test/source/" + id,
    destination: "C:\\Downloads\\" + id + ".zip",
    total: 1024, completed: status === "completed" ? 1024 : 0, priority: 0,
    createdAt, updatedAt: createdAt,
  };
}

async function emit(page: Page, events: { name: string; data?: unknown }[]) {
  await page.evaluate((events) => {
    const { _wails } = window as typeof window & { _wails: { dispatchWailsEvent(event: unknown): void } };
    for (const event of events) _wails.dispatchWailsEvent(event);
  }, events);
}

let eventSequence = 0;

function download(type: string, item: ReturnType<typeof task>, sequence = ++eventSequence) {
  return { name: "download", data: { type, item, sequence } };
}

test("opens later matches with the keyboard and operates checked tasks across queue pages", async ({ page }) => {
  const running = task(1, "running");
  const older = task(51, "paused");
  const items = Array.from({ length: 105 }, (_, i) => i === 0 ? running : i === 50 ? older : task(i + 1));
  const actions: unknown[][] = [];
  await mockDesktop(page, {
    List: () => items,
    ApplyTaskAction: (action, ids) => {
      actions.push([action, ids]);
      running.status = "paused";
      return [{ id: running.id }];
    },
  });
  await page.goto("/");
  const row = (index: number) => page.locator("#task-row-" + task(index).id);
  const select = (index: number) => page.getByRole("checkbox", { name: "Select " + task(index).id + ".zip", exact: true });
  const pages = page.getByRole("navigation", { name: "Download pages" });
  await expect(pages).toContainText("1–50 of 105");
  await expect(page.getByRole("list", { name: "All downloads" }).getByRole("listitem")).toHaveCount(50);
  await select(1).press("Space");
  await row(50).focus();
  await page.keyboard.press("ArrowDown");
  await expect(pages).toContainText("51–100 of 105");
  await expect(row(51)).toBeFocused();
  await expect(row(1)).toHaveCount(0);
  await select(51).press("Space");
  await expect(page.getByText("2 selected", { exact: true })).toBeVisible();

  const detailsButton = page.getByRole("button", { name: "Details for task-0051.zip" });
  await detailsButton.press("Enter");
  const details = page.getByRole("dialog", { name: "Download details" });
  await expect(details).toContainText("task-0051.zip");
  await emit(page, [download("progress", { ...running, completed: 512 })]);
  await page.keyboard.press("Escape");
  await expect(detailsButton).toBeFocused();

  await pages.getByRole("button", { name: "Previous page" }).press("Enter");
  await expect(pages).toContainText("1–50 of 105");
  await expect(row(1)).toContainText("50%");
  await expect(select(1)).toBeChecked();
  await page.getByRole("button", { name: "Pause (1 of 2)" }).click();
  await expect(page.getByRole("region", { name: "Bulk action results" })).toContainText("1 succeeded, 0 failed, 1 skipped");
  expect(actions).toEqual([["pause", [running.id]]]);
  await page.getByRole("checkbox", { name: "Select all matching tasks" }).check();
  await expect(page.getByText("105 selected", { exact: true })).toBeVisible();

  const search = page.getByRole("textbox", { name: "Search downloads" });
  await search.fill("SOURCE/TASK-0105");
  await expect(pages).toHaveCount(0);
  await expect(select(105)).not.toBeChecked();
  await expect(page.getByText("1 item", { exact: true })).toBeVisible();
  await expect(search).toBeFocused();
  await page.getByRole("button", { name: /^Completed/ }).click();
  await emit(page, [download("updated", { ...task(105), status: "failed" })]);
  await expect(page.getByText("0 items", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: /^Failed/ }).click();
  await expect(select(105)).toBeVisible();
});

test("retains task changes and removals that arrive while the queue resyncs", async ({ page }) => {
  const running = task(1, "running");
  const removed = task(2);
  const added = task(3, "paused");
  const previous = [running, removed];
  let finishRefresh!: () => void;
  let startedRefresh!: () => void;
  const refreshStarted = new Promise<void>((resolve) => { startedRefresh = resolve; });
  let holdList = false;

  await mockDesktop(page, {
    List: () => {
      if (!holdList) return previous;
      return new Promise((resolve) => {
        finishRefresh = () => resolve(previous);
        startedRefresh();
      });
    },
  });
  await page.goto("/");
  await expect(page.getByRole("checkbox", { name: "Select task-0002.zip" })).toBeVisible();
  holdList = true;
  await emit(page, [{ name: "resync" }]);
  await refreshStarted;
  await emit(page, [
    download("progress", { ...running, completed: 256 }),
    download("deleted", removed),
    download("updated", added),
    download("progress", { ...running, completed: 512 }),
  ]);
  const getRunning = () => page.locator("#task-row-" + running.id);
  await expect(getRunning()).toContainText("50%");
  const response = page.waitForResponse((response) => response.url().endsWith("/wails/runtime"));
  finishRefresh();
  await (await response).finished();
  await page.evaluate(() => new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))));

  await expect(page.getByRole("checkbox", { name: "Select task-0002.zip" })).toHaveCount(0);
  await expect(page.getByRole("checkbox", { name: "Select task-0003.zip" })).toBeVisible();
  await expect(getRunning()).toContainText("50%");
});

test("preserves keyboard focus when a dark Chinese queue loses its last page", async ({ page }) => {
  const items = Array.from({ length: 51 }, (_, i) => task(i + 1));
  await page.emulateMedia({ colorScheme: "dark" });
  await mockDesktop(page, {
    Settings: () => ({ desktop: { language: "zh-CN" } }),
    List: () => items,
  });
  await page.goto("/");
  const pages = page.getByRole("navigation", { name: "下载列表分页" });
  await expect(pages).toHaveCSS("color", "rgb(163, 163, 179)");
  const next = pages.getByRole("button", { name: "下一页" });
  await next.press("Enter");
  await expect(pages).toContainText("第 51–51 项，共 51 项");
  await expect(next).toBeFocused();

  await emit(page, [download("deleted", items[50])]);
  await expect(pages).toHaveCount(0);
  await expect(page.getByRole("textbox", { name: "搜索下载" })).toBeFocused();
});

test("returns focus to search when an arriving task displaces an open row menu", async ({ page }) => {
  const items = Array.from({ length: 50 }, (_, i) => task(i + 1));
  await mockDesktop(page, { List: () => items });
  await page.goto("/");
  const button = page.getByRole("button", { name: "More actions for task-0050.zip" });
  await button.press("Enter");
  const menu = page.getByRole("menu", { name: "More actions for task-0050.zip" });
  await expect(menu.getByRole("menuitem", { name: "Download details" })).toBeFocused();

  await emit(page, [download("updated", task(0))]);

  await expect(page.getByRole("navigation", { name: "Download pages" })).toContainText("1–50 of 51");
  await expect(button).toHaveCount(0);
  await expect(menu).toHaveCount(0);
  await expect(page.getByRole("textbox", { name: "Search downloads" })).toBeFocused();
});

test("uses snapshot order and skips replay of events already covered by the snapshot", async ({ page }) => {
  const running = task(1, "running");
  const retained = task(2, "paused");
  const removed = task(3);
  const departed = task(4, "paused");
  const appended = task(5, "paused");
  const late = task(6, "paused");
  type Snapshot = { items: ReturnType<typeof task>[]; sequence: number };
  const initial: Snapshot = { items: [running, retained, removed], sequence: 10 };
  const replies: ((snapshot: Snapshot) => void)[] = [];
  let hold = false;
  const snapshot = () => hold ? new Promise<Snapshot>((resolve) => { replies.push(resolve); }) : Promise.resolve(initial);

  await mockDesktop(page, {
    List: async () => (await snapshot()).items,
    QueueSnapshot: snapshot,
  });
  await page.goto("/");
  const row = (index: number) => page.locator("#task-row-" + task(index).id);
  await expect(row(3)).toBeVisible();
  hold = true;
  await emit(page, [{ name: "resync" }]);
  await expect.poll(() => replies.length).toBe(1);
  await emit(page, [{ name: "resync" }]);
  await expect.poll(() => replies.length).toBe(2);

  await emit(page, [
    download("progress", { ...running, completed: 256 }, 12),
    download("updated", departed, 15),
    download("updated", appended, 24),
    download("deleted", removed, 25),
  ]);
  await expect(row(1)).toContainText("25%");
  await expect(row(5)).toBeVisible();

  async function resolveRefresh(index: number, result: Snapshot) {
    const response = page.waitForResponse((response) => response.url().endsWith("/wails/runtime"));
    replies[index](result);
    await (await response).finished();
    await page.evaluate(() => new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))));
  }

  // The newer call reaches the manager first, but its response is delivered last.
  await resolveRefresh(0, { sequence: 22, items: [{ ...running, completed: 768 }, retained, removed] });
  await expect(row(1)).toContainText("75%");
  await expect(row(2)).toBeVisible();
  await expect(row(3)).toHaveCount(0);
  await expect(row(4)).toHaveCount(0);
  await expect(row(5)).toBeVisible();
  await resolveRefresh(1, { sequence: 20, items: [{ ...running, completed: 512 }, retained, departed, removed] });
  await expect(row(1)).toContainText("75%");
  await expect(row(2)).toBeVisible();
  await expect(row(3)).toHaveCount(0);
  await expect(row(4)).toHaveCount(0);
  await expect(row(5)).toBeVisible();
  await emit(page, [{ name: "resync" }]);
  await expect.poll(() => replies.length).toBe(3);
  await resolveRefresh(2, { sequence: 30, items: [{ ...running, completed: 768 }, retained, appended] });
  // The native event queue still contains an add for a task deleted before the snapshot.
  await emit(page, [download("updated", late, 27)]);
  await expect(row(6)).toHaveCount(0);
  await emit(page, [download("progress", { ...running, completed: 896 }, 31)]);
  await expect(row(1)).toContainText("87%");
});

test("returns focus to search when a download dialog's row moves off-page", async ({ page }) => {
  const items = Array.from({ length: 50 }, (_, i) => task(i + 1));
  await mockDesktop(page, { List: () => items });
  await page.goto("/");
  const button = page.getByRole("button", { name: "Details for task-0050.zip" });
  await button.press("Enter");
  const dialog = page.getByRole("dialog", { name: "Download details" });
  await expect(dialog).toBeVisible();

  await emit(page, [download("updated", task(0))]);
  await expect(page.locator("#task-row-task-0050")).toHaveCount(0);
  await expect(dialog).toContainText("task-0050.zip");
  await page.keyboard.press("Escape");
  await expect(dialog).toHaveCount(0);
  await expect(page.getByRole("textbox", { name: "Search downloads" })).toBeFocused();
});
