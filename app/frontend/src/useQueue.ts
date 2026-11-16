import { useCallback, useEffect, useRef, useState } from "react";
import { Events } from "@wailsio/runtime";
import { Desktop, EventType, Item, Status, type Event as DownloadEvent } from "./api";

type Sample = { at: number; done: number; bps: number };

/** Reconcile sequenced queue snapshots and events, including progress rates. */
export function useQueue(onError: (err: unknown) => void) {
  const [items, setItems] = useState<Map<string, Item>>(new Map());
  const [history, setHistory] = useState<number[]>(() => Array(60).fill(0));
  const samples = useRef(new Map<string, Sample>());
  const queueSync = useRef({
    snapshotSequence: 0,
    eventSequence: 0,
    pending: new Set<symbol>(),
    changes: new Map<string, DownloadEvent>(),
  });

  const reload = useCallback(async () => {
    const sync = queueSync.current;
    const request = Symbol();
    sync.pending.add(request);
    try {
      const { items: list, sequence } = await Desktop.QueueSnapshot();
      if (!sync.pending.has(request) || sequence < sync.snapshotSequence) return;
      const next = new Map((list ?? []).map((item) => [item.id, item]));
      // A delayed response can overlap events on either side of its snapshot.
      for (const [id, change] of sync.changes) {
        if ((change.sequence ?? 0) <= sequence) continue;
        if (change.type === EventType.EventDeleted) next.delete(id);
        else next.set(id, change.item);
      }
      sync.snapshotSequence = sequence;
      for (const id of samples.current.keys()) {
        if (next.get(id)?.status !== Status.StatusRunning) samples.current.delete(id);
      }
      setItems(next);
    } catch (err) {
      if (sync.pending.has(request)) onError(err);
    } finally {
      sync.pending.delete(request);
      if (!sync.pending.size) sync.changes.clear();
    }
  }, [onError]);

  useEffect(() => {
    const offDownload = Events.On("download", (ev) => {
      const e = ev.data;
      const sequence = e.sequence ?? 0;
      const sync = queueSync.current;
      if (sequence <= sync.snapshotSequence || sequence <= sync.eventSequence) return;
      sync.eventSequence = sequence;
      const item = e.item;
      if (sync.pending.size) sync.changes.set(item.id, e);
      if (e.type === EventType.EventDeleted) {
        samples.current.delete(item.id);
        setItems((prev) => {
          const next = new Map(prev);
          next.delete(item.id);
          return next;
        });
        return;
      }
      const now = performance.now();
      const last = samples.current.get(item.id);
      if (item.status === Status.StatusRunning) {
        if (last && now > last.at && item.completed >= last.done) {
          const instant = ((item.completed - last.done) * 1000) / (now - last.at);
          samples.current.set(item.id, { at: now, done: item.completed, bps: last.bps ? last.bps * 0.7 + instant * 0.3 : instant });
        } else {
          samples.current.set(item.id, { at: now, done: item.completed, bps: 0 });
        }
      } else {
        samples.current.delete(item.id);
      }
      setItems((prev) => new Map(prev).set(item.id, item));
    });
    const offResync = Events.On("resync", () => reload());
    void reload();
    const timer = window.setInterval(() => {
      const now = performance.now();
      let total = 0;
      for (const [id, s] of samples.current) {
        if (now - s.at > 3000) samples.current.set(id, { ...s, bps: 0 });
        total += s.bps;
      }
      setHistory((h) => [...h.slice(-59), total]);
    }, 1000);
    return () => {
      queueSync.current.pending.clear();
      queueSync.current.changes.clear();
      offDownload();
      offResync();
      window.clearInterval(timer);
    };
  }, [reload]);

  return { items, samples, history, reload };
}
