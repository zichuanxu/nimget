import { expect, test } from "@playwright/test";
import { readFileSync } from "node:fs";
import { mockDesktop } from "./desktop";

test("measures search and status filters under a persisted queue's progress workload", async ({ page, browser }) => {
  const path = process.env.NIMGET_QUEUE_FIXTURE;
  test.skip(!path, "Set NIMGET_QUEUE_FIXTURE to the service integration test's exported snapshot.");
  test.setTimeout(120_000);

  const items = JSON.parse(readFileSync(path!, "utf8"));
  const running = items.filter((item: { status: string }) => item.status === "running");
  const activeCount = running.length;
  const failedCount = items.filter((item: { status: string }) => item.status === "failed").length;
  expect(items).toHaveLength(2000);
  expect(activeCount).toBe(4);
  await mockDesktop(page, { List: () => items });
  await page.addInitScript(() => {
    const longTasks: number[] = [];
    new PerformanceObserver((entries) => {
      longTasks.push(...entries.getEntries().map((entry) => entry.duration));
    }).observe({ type: "longtask", buffered: true });
    Object.assign(window, { longTasks });
  });
  const session = await page.context().newCDPSession(page);
  await session.send("Performance.enable");
  await page.goto("/");
  const search = page.getByRole("textbox", { name: "Search downloads" });
  await expect(page.getByText("2000 items", { exact: true })).toBeVisible();
  await expect(page.getByRole("checkbox", { name: "Select task-0001.zip" })).toBeVisible();
  const startup = await page.evaluate(() => ({
    renderedRows: document.querySelectorAll(".task-rows > li").length,
    elements: document.getElementsByTagName("*").length,
    readyMs: Math.round(performance.now()),
  }));

  await page.evaluate((active) => {
    const host = window as typeof window & {
      _wails: { dispatchWailsEvent(event: unknown): void };
      longTasks: number[];
      workload: Promise<{ ms: number; elapsed: number[]; longTasks: number[]; delivered: number }>;
    };
    host.longTasks.length = 0;
    const start = performance.now();
    const elapsed: number[] = [];
    let lastInteraction = start;
    let delivered = 0;
    const sampleInteraction = (event: Event) => {
      if (!(event.target instanceof Element) || !event.target.closest(".search input, .nav-item")) return;
      const at = performance.now();
      lastInteraction = at;
      // Two animation frames include the first chance to paint the new results.
      requestAnimationFrame(() => requestAnimationFrame(() => {
        if (lastInteraction === at) elapsed.push(performance.now() - at);
      }));
    };
    document.addEventListener("input", sampleInteraction, true);
    document.addEventListener("click", sampleInteraction, true);
    host.workload = new Promise((resolve) => {
      const timer = setInterval(() => {
        const item = active[delivered % active.length];
        item.completed += 64 << 10;
        host._wails.dispatchWailsEvent({ name: "download", data: { type: "progress", item: { ...item }, sequence: delivered + 1 } });
        delivered++;
        if (delivered === 128) {
          clearInterval(timer);
          requestAnimationFrame(() => requestAnimationFrame(() => {
            document.removeEventListener("input", sampleInteraction, true);
            document.removeEventListener("click", sampleInteraction, true);
            resolve({ ms: performance.now() - start, elapsed, longTasks: host.longTasks, delivered });
          }));
        }
      }, 25);
    });
  }, running);

  const before = await session.send("Performance.getMetrics");
  await search.fill("task-0001.zip");
  await expect(page.getByText("1 item", { exact: true })).toBeVisible();
  await search.fill("TASK-01");
  await expect(page.getByText("100 items", { exact: true })).toBeVisible();
  await search.clear();
  await expect(page.getByText("2000 items", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: /^Failed/ }).click();
  await expect(page.getByText(failedCount + " items", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: /^Active/ }).click();
  await expect(page.getByText(activeCount + " items", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: /^All downloads/ }).click();
  await expect(page.getByText("2000 items", { exact: true })).toBeVisible();

  const result = await page.evaluate(() => (window as Window & { workload?: Promise<{
    ms: number; elapsed: number[]; longTasks: number[]; delivered: number;
  }> }).workload!);
  const after = await session.send("Performance.getMetrics");
  const metricMs = (name: string) => {
    const beforeValue = before.metrics.find((metric) => metric.name === name)?.value ?? 0;
    const afterValue = after.metrics.find((metric) => metric.name === name)?.value ?? 0;
    return Math.round((afterValue - beforeValue) * 1000);
  };

  expect(result.delivered).toBe(128);
  const topRow = page.getByRole("listitem").filter({ has: page.getByRole("checkbox", { name: "Select task-0001.zip" }) });
  const done = running.find((item: { id: string }) => item.id === "task-0001").completed + (32 * (64 << 10));
  await expect(topRow).toContainText(Math.floor(100 * done / (16 << 20)) + "%");
  console.log(JSON.stringify({
    chromium: browser.version(),
    ...startup,
    workloadMs: Math.round(result.ms),
    updatesDelivered: result.delivered,
    inputToFrameMs: result.elapsed.map(Math.round),
    longTasks: result.longTasks.length,
    longestTaskMs: Math.round(Math.max(0, ...result.longTasks)),
    taskMs: metricMs("TaskDuration"),
    scriptMs: metricMs("ScriptDuration"),
    layoutMs: metricMs("LayoutDuration"),
    styleMs: metricMs("RecalcStyleDuration"),
  }));
});
