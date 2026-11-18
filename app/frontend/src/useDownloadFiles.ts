import { useCallback, useEffect, useState } from "react";
import { Desktop, Item, Status, type MissingDownloadFile } from "./api";

const checkIntervalMs = 5000;

export function useDownloadFiles(rows: Item[], details?: Item) {
  const [missing, setMissing] = useState<Partial<Record<string, MissingDownloadFile>>>({});
  const ids = rows.filter((item) => item.status === Status.StatusCompleted).map((item) => item.id);
  if (details?.status === Status.StatusCompleted && !ids.includes(details.id)) ids.push(details.id);
  // Progress events can replace the queue without changing which files to check.
  const scope = JSON.stringify(ids);

  useEffect(() => {
    const taskIDs: string[] = JSON.parse(scope);
    if (!taskIDs.length) return;
    let disposed = false;
    let request: ReturnType<typeof Desktop.MissingDownloadFiles> | undefined;

    const refresh = async () => {
      if (document.hidden || request) return;
      request = Desktop.MissingDownloadFiles(taskIDs);
      try {
        const result = await request;
        if (!disposed) setMissing(result);
      } catch {
        // Keep the last result if the check fails and try again on the next refresh.
      } finally {
        request = undefined;
      }
    };

    void refresh();
    const timer = window.setInterval(refresh, checkIntervalMs);
    window.addEventListener("focus", refresh);
    document.addEventListener("visibilitychange", refresh);
    return () => {
      disposed = true;
      request?.cancel();
      window.clearInterval(timer);
      window.removeEventListener("focus", refresh);
      document.removeEventListener("visibilitychange", refresh);
    };
  }, [scope]);

  return useCallback((item: Item) => {
    const file = missing[item.id];
    if (item.status === Status.StatusCompleted && file?.destination === item.destination) return file;
  }, [missing]);
}
