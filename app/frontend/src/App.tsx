import { memo, useCallback, useEffect, useId, useLayoutEffect, useMemo, useRef, useState } from "react";
import { Browser, Events } from "@wailsio/runtime";
import {
  ArrowDownToLine,
  CircleAlert,
  CircleCheck,
  Clipboard,
  Ellipsis,
  FileArchive,
  FileImage,
  FileMusic,
  FilePlay,
  FileText,
  File as FileIcon,
  Film,
  FolderInput,
  FolderOpen,
  FolderOutput,
  Gauge,
  Hourglass,
  Inbox,
  Info,
  KeyRound,
  Layers,
  Package,
  Pause,
  Play,
  Plus,
  RotateCw,
  Search,
  Settings as SettingsIcon,
  Sparkles,
  SquareArrowOutUpRight,
  Trash2,
  X,
  type LucideIcon,
} from "lucide-react";
import { Desktop, Item, Recovery, State, Status, message } from "./api";
import AddDialog, { type AddPrefill } from "./AddDialog";
import BulkTaskActions from "./BulkTaskActions";
import SettingsDialog from "./SettingsDialog";
import TaskDetailsDialog from "./TaskDetailsDialog";
import { CountdownDialog, DeleteDialog, ReauthDialog } from "./dialogs";
import Sparkline from "./Sparkline";
import { baseName, bytes, eta, fileKind, looksLikeCurl, looksLikeURL, speed, type FileKind } from "./format";
import { I18n, resolveLang, translator, useI18n, type T } from "./i18n";
import { useQueue } from "./useQueue";
import { useDownloadFiles } from "./useDownloadFiles";
import { taskFailure } from "./taskFailure";
import { Menu, Modal, type MenuEntry } from "./ui";

type Filter = "all" | "active" | "paused" | "completed" | "failed";
type Toast = { kind: "ok" | "error"; text: string };
type Action = Exclude<MenuEntry, "separator">;

const kindIcon: Record<FileKind, LucideIcon> = {
  video: FilePlay,
  audio: FileMusic,
  archive: FileArchive,
  image: FileImage,
  document: FileText,
  program: Package,
  file: FileIcon,
};

const filters: { id: Filter; icon: LucideIcon; label: "nav.all" | "nav.active" | "nav.paused" | "nav.completed" | "nav.failed" }[] = [
  { id: "all", icon: Layers, label: "nav.all" },
  { id: "active", icon: ArrowDownToLine, label: "nav.active" },
  { id: "paused", icon: Pause, label: "nav.paused" },
  { id: "completed", icon: CircleCheck, label: "nav.completed" },
  { id: "failed", icon: CircleAlert, label: "nav.failed" },
];

function matches(item: Item, f: Filter): boolean {
  switch (f) {
    case "active":
      return item.status === Status.StatusRunning || item.status === Status.StatusQueued;
    case "paused":
      return item.status === Status.StatusPaused;
    case "completed":
      return item.status === Status.StatusCompleted;
    case "failed":
      return item.status === Status.StatusFailed;
  }
  return true;
}

const tasksPerPage = 50;
const isMac = navigator.userAgent.includes("Mac");

/** App owns the interface language and renders the window. */
export default function App() {
  const [langSetting, setLangSetting] = useState<string>();
  const lang = resolveLang(langSetting);
  const i18n = useMemo(() => ({ lang, t: translator(lang), setLang: setLangSetting }), [lang]);
  useEffect(() => {
    Desktop.Settings()
      .then((s) => setLangSetting(s.desktop.language ?? ""))
      .catch(() => setLangSetting(""));
  }, []);
  useEffect(() => {
    document.documentElement.lang = lang;
    Desktop.SetLocale(lang).catch(() => {});
  }, [lang]);
  return (
    <I18n.Provider value={i18n}>
      <Shell />
    </I18n.Provider>
  );
}

function Shell() {
  const { t } = useI18n();
  const [state, setState] = useState<State | null>(null);
  const [selected, setSelected] = useState<string | null>(null);
  const [selectedTaskIDs, setSelectedTaskIDs] = useState<Set<string>>(() => new Set());
  const [bulkConfirming, setBulkConfirming] = useState<Item[] | null>(null);
  const [pageIndex, setPageIndex] = useState(0);
  const [filter, setFilter] = useState<Filter>("all");
  const [query, setQuery] = useState("");
  const [adding, setAdding] = useState<AddPrefill | null>(null);
  const [retainAddDraft, setRetainAddDraft] = useState(false);
  const [addFormKey, setAddFormKey] = useState(0);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [backupOpen, setBackupOpen] = useState(false);
  const [legacyImportID, setLegacyImportID] = useState<string | null>(null);
  const [deleting, setDeleting] = useState<Item | null>(null);
  const [reauth, setReauth] = useState<Item | null>(null);
  const [detailsId, setDetailsId] = useState<string | null>(null);
  const [clip, setClip] = useState<string | null>(null);
  const [toast, setToast] = useState<Toast | null>(null);
  const [canConvert, setCanConvert] = useState(false);
  const [converting, setConverting] = useState<string | null>(null);
  const [countdown, setCountdown] = useState<{ action: string; left: number } | null>(null);
  const [dragging, setDragging] = useState(false);
  const [context, setContext] = useState<{ x: number; y: number; id: string; trigger: HTMLElement } | null>(null);
  const contextMenuId = useId();
  const [dismissed, setDismissed] = useState(() => {
    try {
      return localStorage.getItem("dismissedUpdate") ?? "";
    } catch {
      return "";
    }
  });
  const listElement = useRef<HTMLDivElement>(null);
  const rowToFocus = useRef<string | null>(null);
  const newDownload = useRef<HTMLButtonElement>(null);
  const search = useRef<HTMLInputElement>(null);
  const focusSearch = useCallback(() => search.current?.focus({ preventScroll: true }), []);
  const { ref: pageControls } = useFocusFallbackOnRemove<HTMLElement>(focusSearch);
  const dragDepth = useRef(0);

  const fail = useCallback((err: unknown) => setToast({ kind: "error", text: message(err) }), []);
  const ok = useCallback((text: string) => setToast({ kind: "ok", text }), []);

  const { items, samples, history, reload } = useQueue(fail);

  useEffect(() => {
    Desktop.State().then(setState).catch(fail);
    Desktop.CanConvert().then(setCanConvert).catch(() => {});
    const offStopped = Events.On("stopped", () => Desktop.State().then(setState).catch(fail));
    const offClip = Events.On("clipboard", (ev) => setClip(ev.data));
    const offDone = Events.On("queue-done", (ev) => setCountdown({ action: ev.data, left: 30 }));
    const offUpdate = Events.On("update", () => Desktop.State().then(setState).catch(() => {}));
    return () => {
      offStopped();
      offClip();
      offDone();
      offUpdate();
    };
  }, [fail]);

  // The completion action runs when the countdown reaches zero unless cancelled.
  useEffect(() => {
    if (!countdown) return;
    if (countdown.left <= 0) {
      setCountdown(null);
      Desktop.PerformWhenDone(countdown.action).catch(fail);
      return;
    }
    const timer = window.setTimeout(() => setCountdown({ ...countdown, left: countdown.left - 1 }), 1000);
    return () => window.clearTimeout(timer);
  }, [countdown, fail]);

  useEffect(() => {
    if (!toast) return;
    const timer = window.setTimeout(() => setToast(null), toast.kind === "error" ? 8000 : 3500);
    return () => window.clearTimeout(timer);
  }, [toast]);

  const reportImport = (res: Awaited<ReturnType<typeof Desktop.ImportQueue>>) => {
    if (res.problem === "summary" || res.problem === "invalid" || res.problem === "unreadable" || res.problem === "expired") {
      fail(t(`import.${res.problem}`));
    } else if (res.failed?.length) {
      const failed = res.failed.map((n) => t("import.entry", { n })).join("\n");
      fail(t("toast.imported", { n: res.added, failed }));
    } else if (res.added > 0) {
      ok(t("import.added", { n: res.added }));
    }
  };

  const importQueue = async () => {
    try {
      const res = await Desktop.ImportQueue();
      if (res.legacyId) setLegacyImportID(res.legacyId);
      else reportImport(res);
    } catch (err) {
      fail(err);
    }
  };

  const finishLegacyImport = (confirm: boolean) => {
    if (!legacyImportID) return;
    setLegacyImportID(null);
    Desktop.FinishLegacyImport(legacyImportID, confirm).then(reportImport).catch(fail);
  };

  const exportQueue = async (save: () => Promise<string>) => {
    try {
      if (await save()) ok(t("toast.exported"));
    } catch (err) {
      fail(err);
    }
  };

  const run = useCallback((fn: () => Promise<unknown>) => () => {
    fn().catch(fail);
  }, [fail]);

  const convert = useCallback((item: Item) => {
    setConverting(item.id);
    Desktop.ConvertToMP4(item.id)
      .then((out) => ok(t("toast.saved", { name: baseName(out) })))
      .catch(fail)
      .finally(() => setConverting(null));
  }, [ok, fail, t]);

  // RFC 3339 strings with trimmed fractions do not sort as text.
  const all = useMemo(() => [...items.values()].sort((a, b) => Date.parse(b.createdAt) - Date.parse(a.createdAt)), [items]);
  const counts = useMemo(() => Object.fromEntries(filters.map((f) => [f.id, all.filter((i) => matches(i, f.id)).length])) as Record<Filter, number>, [all]);
  const q = query.trim().toLowerCase();
  const rows = useMemo(
    () => all.filter((i) => matches(i, filter) && (!q || baseName(i.destination).toLowerCase().includes(q) || i.url.toLowerCase().includes(q))),
    [all, filter, q],
  );
  const lastPage = Math.max(0, Math.ceil(rows.length / tasksPerPage) - 1);
  const page = Math.min(pageIndex, lastPage);
  const pageStart = page * tasksPerPage;
  const pageRows = rows.slice(pageStart, pageStart + tasksPerPage);
  useLayoutEffect(() => {
    if (listElement.current) listElement.current.scrollTop = 0;
    if (rowToFocus.current) {
      document.getElementById(taskRowId(rowToFocus.current))?.focus();
      rowToFocus.current = null;
    }
  }, [page, filter, q]);
  useEffect(() => {
    if (page !== pageIndex) setPageIndex(page);
  }, [page, pageIndex]);

  const selectedTasks = useMemo(
    () => selectedTaskIDs.size ? rows.filter((item) => selectedTaskIDs.has(item.id)) : [],
    [rows, selectedTaskIDs],
  );
  useEffect(() => {
    if (selectedTasks.length !== selectedTaskIDs.size) {
      setSelectedTaskIDs(new Set(selectedTasks.map((item) => item.id)));
    }
  }, [selectedTasks, selectedTaskIDs.size]);

  const changeQuery = (value: string) => {
    setQuery(value);
    setPageIndex(0);
    setSelected(null);
    setSelectedTaskIDs(new Set());
  };
  const current = selected && selectedTasks.length === 0 ? pageRows.find((item) => item.id === selected) : undefined;
  const totalSpeed = history[history.length - 1] ?? 0;
  const details = detailsId ? items.get(detailsId) : undefined;
  const missingFile = useDownloadFiles(pageRows, details);
  const fileNotice = useCallback((item: Item) => {
    const file = missingFile(item);
    if (file) return t(file.folderMissing ? "row.folderMissing" : "row.fileMissing");
  }, [missingFile, t]);
  const contextItem = context ? pageRows.find((item) => item.id === context.id) : undefined;
  const modalOpen = !!(adding || settingsOpen || deleting || reauth || countdown || details || bulkConfirming || backupOpen || legacyImportID);
  useEffect(() => {
    if (context && !contextItem) setContext(null);
  }, [context, contextItem]);

  const checkTask = useCallback((id: string, checked: boolean) => {
    setSelectedTaskIDs((previous) => {
      const next = new Set(previous);
      if (checked) next.add(id);
      else next.delete(id);
      return next;
    });
  }, []);
  const showContext = useCallback((id: string, x: number, y: number, trigger: HTMLElement) => {
    setSelected(id);
    setContext((open) => {
      if (open?.trigger === trigger && trigger instanceof HTMLButtonElement) return null;
      return { x, y, id, trigger };
    });
  }, []);
  const openTask = useCallback((item: Item) => {
    const notice = fileNotice(item);
    if (notice) fail(notice);
    else run(() => (item.status === Status.StatusCompleted ? Desktop.Open(item.id) : Desktop.Reveal(item.id)))();
  }, [fail, fileNotice, run]);
  const changePage = (index: number) => {
    if (index < 0 || index > lastPage) return;
    setPageIndex(index);
    setSelected(null);
    setContext(null);
  };

  const actions = useCallback((item: Item): Action[] => {
    const list: Action[] = [];
    const missing = missingFile(item);
    if (item.status === Status.StatusRunning || item.status === Status.StatusQueued) list.push({ icon: Pause, label: t("row.pause"), onSelect: run(() => Desktop.Pause(item.id)) });
    if (item.status === Status.StatusPaused) list.push({ icon: Play, label: t("row.resume"), onSelect: run(() => Desktop.Resume(item.id)) });
    if (item.status === Status.StatusFailed && item.recovery !== Recovery.RecoveryRestartRequired)
      list.push({ icon: RotateCw, label: t("row.retry"), onSelect: run(() => Desktop.Retry(item.id)) });
    if (item.status === Status.StatusFailed || item.status === Status.StatusPaused)
      list.push({
        icon: KeyRound,
        label: t(item.recovery === Recovery.RecoveryRestartRequired ? "row.recover" : "row.reauth"),
        onSelect: () => setReauth(item),
      });
    if (item.status === Status.StatusCompleted && !missing) {
      list.push({ icon: SquareArrowOutUpRight, label: t("row.open"), onSelect: run(() => Desktop.Open(item.id)) });
      if (canConvert && /\.ts$/i.test(item.destination) && converting !== item.id)
        list.push({ icon: Film, label: t("row.convert"), onSelect: () => convert(item) });
    }
    if (!missing?.folderMissing) list.push({ icon: FolderOpen, label: t("row.reveal"), onSelect: run(() => Desktop.Reveal(item.id)) });
    list.push({ icon: Trash2, label: t("row.delete"), onSelect: () => setDeleting(item), danger: true });
    return list;
  }, [canConvert, converting, convert, missingFile, run, t]);

  // Keyboard: ⌘/Ctrl+N add, ⌘/Ctrl+, settings, ⌘/Ctrl+F search, arrows move
  // the selection, Space pauses or resumes, Enter opens, Delete removes.
  useLayoutEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (modalOpen || contextItem || e.defaultPrevented || e.isComposing) return;
      const target = e.target instanceof HTMLElement ? e.target : null;
      if (target?.isContentEditable || target?.closest("input, textarea, select")) {
        if (e.key === "Escape" && target === search.current) target.blur();
        return;
      }
      const mod = isMac ? e.metaKey : e.ctrlKey;
      if (mod && e.key.toLowerCase() === "n") {
        e.preventDefault();
        setAdding({});
      } else if (mod && e.key === ",") {
        e.preventDefault();
        setSettingsOpen(true);
      } else if (mod && e.key.toLowerCase() === "f") {
        e.preventDefault();
        search.current?.focus();
      } else if (target?.closest("button, a, summary")) {
        // Keep the native keyboard behavior of focused controls.
        return;
      } else if (e.key === "ArrowDown" || e.key === "ArrowUp") {
        const i = pageRows.findIndex((r) => r.id === selected);
        const offset = e.key === "ArrowDown" ? 1 : -1;
        const nextIndex = i < 0 ? pageStart : Math.max(0, Math.min(rows.length - 1, pageStart + i + offset));
        const next = rows[nextIndex];
        if (next) {
          e.preventDefault();
          setSelected(next.id);
          const nextPage = Math.floor(nextIndex / tasksPerPage);
          if (nextPage === page) document.getElementById(taskRowId(next.id))?.focus();
          else {
            rowToFocus.current = next.id;
            setPageIndex(nextPage);
          }
        }
      } else if (current && e.key === " ") {
        e.preventDefault();
        if (current.status === Status.StatusPaused) run(() => Desktop.Resume(current.id))();
        else if (current.status === Status.StatusRunning || current.status === Status.StatusQueued) run(() => Desktop.Pause(current.id))();
      } else if (current && (e.key === "Delete" || (isMac && e.key === "Backspace" && e.metaKey))) {
        setDeleting(current);
      } else if (current && e.key === "Enter") {
        openTask(current);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  });

  const dropText = (e: React.DragEvent) => {
    const text = e.dataTransfer.getData("text/uri-list") || e.dataTransfer.getData("text/plain");
    const first = text.split(/\r?\n/).find((l) => l && !l.startsWith("#")) ?? "";
    return looksLikeCurl(text) ? text : looksLikeURL(first) ? first : "";
  };

  const closeContext = useCallback(() => setContext(null), []);
  const title = t(filters.find((f) => f.id === filter)!.label);

  return (
    <div
      className="shell"
      onDragEnter={(e) => {
        if (modalOpen || !e.dataTransfer.types.some((ty) => ty === "text/uri-list" || ty === "text/plain")) return;
        dragDepth.current++;
        setDragging(true);
      }}
      onDragLeave={() => {
        dragDepth.current = Math.max(0, dragDepth.current - 1);
        if (!dragDepth.current) setDragging(false);
      }}
      onDragOver={(e) => e.preventDefault()}
      onDrop={(e) => {
        dragDepth.current = 0;
        setDragging(false);
        const text = dropText(e);
        if (text && !modalOpen) {
          e.preventDefault();
          setAdding({ text });
        }
      }}
    >
      <aside className={`sidebar ${isMac ? "mac" : ""}`}>
        <div className="brand drag">
          <img className="logo" src="/nimget.png" alt="" />
          <span className="brand-name">NimGet</span>
        </div>
        <button ref={newDownload} className="primary block" onClick={() => setAdding({})} title={`${t("app.newDownload")} (${isMac ? "⌘" : "Ctrl+"}N)`}>
          <Plus size={16} strokeWidth={2.4} />
          {t("app.newDownload")}
        </button>
        <nav className="nav">
          {filters.map((f) => (
            <button key={f.id} className={`nav-item ${filter === f.id ? "on" : ""} ${f.id}`} aria-current={filter === f.id || undefined} onClick={() => {
              if (filter === f.id) return;
              setFilter(f.id);
              setPageIndex(0);
              setSelected(null);
              setSelectedTaskIDs(new Set());
            }}>
              <f.icon size={16} />
              <span className="grow">{t(f.label)}</span>
              {counts[f.id] > 0 && <span className="count">{counts[f.id]}</span>}
            </button>
          ))}
        </nav>
        <div className="grow" />
        <SpeedCard
          running={all.filter((i) => i.status === Status.StatusRunning)}
          queued={all.filter((i) => i.status === Status.StatusQueued).length}
          rate={(id) => samples.current.get(id)?.bps ?? 0}
          bps={totalSpeed}
          history={history}
        />
        <button className="nav-item" onClick={() => setSettingsOpen(true)} title={`${t("nav.settings")} (${isMac ? "⌘" : "Ctrl+"},)`}>
          <SettingsIcon size={16} />
          <span className="grow">{t("nav.settings")}</span>
          {state && <span className="version">{/^\d/.test(state.version) ? `v${state.version}` : state.version}</span>}
        </button>
      </aside>

      <section className="main">
        <header className="topbar drag">
          <div className="title">
            <h1>{title}</h1>
            <span className="subtitle">{rows.length === 1 ? t("header.count1") : t("header.count", { n: rows.length })}</span>
          </div>
          <div className="grow" />
          <div className="search no-drag">
            <Search size={14} />
            <input ref={search} value={query} onChange={(e) => changeQuery(e.target.value)} placeholder={t("header.search")} aria-label={t("header.search")} spellCheck={false} />
            {query && (
              <button className="icon ghost tiny" onClick={() => changeQuery("")} aria-label={t("common.close")}>
                <X size={12} />
              </button>
            )}
          </div>
          <div className="tools no-drag">
            <button className="icon" title={t("header.resumeAll")} aria-label={t("header.resumeAll")} disabled={!state || !!state.error || counts.paused === 0} onClick={run(() => Desktop.ResumeAll())}>
              <Play size={16} />
            </button>
            <button className="icon" title={t("header.pauseAll")} aria-label={t("header.pauseAll")} disabled={!state || !!state.error || counts.active === 0} onClick={run(() => Desktop.PauseAll())}>
              <Pause size={16} />
            </button>
            <Menu
              trigger={(open) => (
                <button className={`icon ${open ? "pressed" : ""}`} title={t("header.more")} aria-label={t("header.more")}>
                  <Ellipsis size={16} />
                </button>
              )}
              entries={[
                { icon: FolderInput, label: t("header.import"), onSelect: importQueue },
                { icon: FolderOutput, label: t("header.export"), onSelect: () => void exportQueue(Desktop.ExportQueue) },
                { icon: KeyRound, label: t("header.backup"), onSelect: () => setBackupOpen(true) },
              ]}
            />
          </div>
        </header>

        {state?.error && (
          <div className="banner danger">
            <CircleAlert size={16} />
            <span className="grow">{t("banner.serviceError", { error: state.error })}</span>
          </div>
        )}
        {state?.update && state.update.version !== dismissed && (
          <div className="banner info">
            <Sparkles size={16} />
            <span className="grow">{t("banner.update", { version: state.update.version })}</span>
            <button className="small" onClick={() => Browser.OpenURL(state.update!.url)}>
              {t("banner.releaseNotes")}
            </button>
            <button
              className="icon ghost tiny"
              title={t("banner.dismiss")}
              aria-label={t("banner.dismiss")}
              onClick={() => {
                const v = state.update!.version;
                setDismissed(v);
                try {
                  localStorage.setItem("dismissedUpdate", v);
                } catch {
                  // per-viewer convenience only
                }
              }}
            >
              <X size={14} />
            </button>
          </div>
        )}

        <BulkTaskActions
          visibleCount={rows.length}
          multiplePages={lastPage > 0}
          selected={selectedTasks}
          disabled={!state || !!state.error}
          onSelectAll={(checked) => setSelectedTaskIDs(checked ? new Set(rows.map((item) => item.id)) : new Set())}
          confirming={bulkConfirming}
          onConfirm={setBulkConfirming}
          refresh={reload}
        />

        {lastPage > 0 && (
          <nav ref={pageControls} className="task-pages" aria-label={t("queue.pages")}>
            <span role="status">{t("queue.range", { from: pageStart + 1, to: pageStart + pageRows.length, total: rows.length })}</span>
            <button className="small" aria-disabled={page === 0} onClick={() => changePage(page - 1)}>{t("queue.previous")}</button>
            <button className="small" aria-disabled={page === lastPage} onClick={() => changePage(page + 1)}>{t("queue.next")}</button>
          </nav>
        )}
        <div ref={listElement} className="list" onMouseDown={(e) => e.target === e.currentTarget && setSelected(null)}>
          {rows.length === 0 ? (
            <Empty all={all.length} query={query} onAdd={() => setAdding({})} />
          ) : (
            <>
            <div className="task-columns" aria-hidden="true">
              <span className="col-status">{t("col.status")}</span>
              <span className="col-file">{t("col.file")}</span>
              <span className="col-progress">{t("col.progress")}</span>
              <span className="col-speed">{t("col.speed")}</span>
              <span className="col-arrival">{t("col.arrival")}</span>
              <span className="col-size" />
            </div>
            <ul className="task-rows" role="list" aria-label={title}>
              {pageRows.map((item) => (
                <Row
                  key={item.id}
                  item={item}
                  fileNotice={fileNotice(item)}
                  bps={samples.current.get(item.id)?.bps ?? 0}
                  current={current?.id === item.id}
                  checked={selectedTaskIDs.has(item.id)}
                  onCheck={checkTask}
                  converting={converting === item.id}
                  actions={actions}
                  menuId={context?.id === item.id ? contextMenuId : undefined}
                  onSelect={setSelected}
                  onDetails={setDetailsId}
                  onOpen={openTask}
                  onContext={showContext}
                  onRemovedFocus={focusSearch}
                />
              ))}
            </ul>
            </>
          )}
        </div>
      </section>

      {context && contextItem && (
        <ContextMenu
          id={contextMenuId}
          label={t("row.actionsFor", { name: baseName(contextItem.destination) })}
          x={context.x}
          y={context.y}
          trigger={context.trigger}
          entries={[{ icon: Info, label: t("details.title"), onSelect: () => setDetailsId(contextItem.id) }, ...actions(contextItem)]}
          onClose={closeContext}
          onRemovedFocus={focusSearch}
        />
      )}

      {dragging && (
        <div className="dropzone">
          <div className="dropzone-card">
            <ArrowDownToLine size={28} />
            <strong>{t("drop.title")}</strong>
            <span>{t("drop.body")}</span>
          </div>
        </div>
      )}

      <div className="floating">
        {clip && (
          <div className="card clip">
            <span className="card-icon">
              <Clipboard size={16} />
            </span>
            <div className="grow clip-body">
              <strong>{t("clip.title")}</strong>
              <span className="clip-text">{looksLikeCurl(clip) ? t("clip.curl") : clip}</span>
            </div>
            <button
              className="primary small"
              onClick={() => {
                newDownload.current?.focus({ preventScroll: true });
                setAdding({ text: clip });
                setClip(null);
              }}
            >
              <ArrowDownToLine size={14} />
              {t("clip.download")}
            </button>
            <button className="icon ghost tiny" onClick={() => setClip(null)} aria-label={t("banner.dismiss")} title={t("banner.dismiss")}>
              <X size={14} />
            </button>
          </div>
        )}
        {toast && (
          <div className={`card toast ${toast.kind}`} onClick={() => setToast(null)} role="status">
            <span className="card-icon">{toast.kind === "error" ? <CircleAlert size={16} /> : <CircleCheck size={16} />}</span>
            <span className="grow toast-text">{toast.text}</span>
          </div>
        )}
      </div>

      {countdown && (
        <CountdownDialog
          action={countdown.action}
          left={countdown.left}
          onCancel={() => {
            setCountdown(null);
            Desktop.SetWhenDone("").catch(fail);
          }}
        />
      )}
      {(adding || retainAddDraft) && state && (
        <AddDialog
          key={addFormKey}
          prefill={adding}
          hasNewPrefill={retainAddDraft && !!adding?.text}
          defaultDirectory={state.defaultDirectory}
          onClose={(keepDraft) => {
            setRetainAddDraft(keepDraft);
            setAdding(null);
          }}
          onStartNew={() => {
            if (!retainAddDraft) setAdding({});
            setRetainAddDraft(false);
            setAddFormKey((key) => key + 1);
          }}
          onError={fail}
        />
      )}
      {legacyImportID && (
        <Modal
          icon={FolderInput}
          tone="warn"
          size="small"
          title={t("import.legacyTitle")}
          onClose={() => finishLegacyImport(false)}
          focusFallback={focusSearch}
          footer={
            <>
              <button type="button" data-initial-focus onClick={() => finishLegacyImport(false)}>{t("common.cancel")}</button>
              <button type="button" className="primary" onClick={() => finishLegacyImport(true)}>{t("import.confirmLegacy")}</button>
            </>
          }
        >
          <p>{t("import.legacyWarning")}</p>
        </Modal>
      )}
      {backupOpen && (
        <Modal
          icon={KeyRound}
          tone="warn"
          size="small"
          title={t("backup.title")}
          onClose={() => setBackupOpen(false)}
          focusFallback={focusSearch}
          footer={
            <>
              <button type="button" data-initial-focus onClick={() => setBackupOpen(false)}>{t("common.cancel")}</button>
              <button type="button" className="primary" onClick={() => {
                setBackupOpen(false);
                void exportQueue(Desktop.BackupQueue);
              }}>{t("backup.save")}</button>
            </>
          }
        >
          <p>{t("backup.warning")}</p>
        </Modal>
      )}
      {settingsOpen && state && (
        <SettingsDialog
          state={state}
          onClose={() => setSettingsOpen(false)}
          onSaved={() => {
            Desktop.State().then(setState);
            Desktop.CanConvert().then(setCanConvert);
          }}
        />
      )}
      {deleting && <DeleteDialog item={items.get(deleting.id) ?? deleting} onClose={() => setDeleting(null)} onError={fail} focusFallback={focusSearch} />}
      {details && (
        <TaskDetailsDialog
          key={details.id}
          item={details}
          fileNotice={fileNotice(details)}
          focusFallback={focusSearch}
          onClose={() => setDetailsId(null)}
          onRecover={() => {
            setDetailsId(null);
            setReauth(details);
          }}
        />
      )}
      {reauth && <ReauthDialog item={items.get(reauth.id) ?? reauth} onClose={() => setReauth(null)} focusFallback={focusSearch} />}
    </div>
  );
}

function useFocusFallbackOnRemove<T extends HTMLElement>(fallback: () => void) {
  const element = useRef<T | null>(null);
  const ref = useCallback((node: T | null) => {
    if (!node && element.current?.contains(document.activeElement)) fallback();
    element.current = node;
  }, [fallback]);
  return { element, ref };
}

function taskRowId(id: string) {
  return "task-row-" + id;
}

const Row = memo(function Row({
  item,
  fileNotice,
  bps,
  current,
  checked,
  onCheck,
  converting,
  actions,
  menuId,
  onSelect,
  onDetails,
  onOpen,
  onContext,
  onRemovedFocus,
}: {
  item: Item;
  fileNotice?: string;
  bps: number;
  current: boolean;
  checked: boolean;
  onCheck: (id: string, checked: boolean) => void;
  converting: boolean;
  actions: (item: Item) => Action[];
  menuId?: string;
  onSelect: (id: string) => void;
  onDetails: (id: string) => void;
  onOpen: (item: Item) => void;
  onContext: (id: string, x: number, y: number, trigger: HTMLElement) => void;
  onRemovedFocus: () => void;
}) {
  const { t, lang } = useI18n();
  const { ref } = useFocusFallbackOnRemove<HTMLLIElement>(onRemovedFocus);
  const name = baseName(item.destination);
  const kind = fileKind(name);
  const Icon = kindIcon[kind];
  const known = item.total > 0;
  const pct = known ? Math.min(100, (item.completed / item.total) * 100) : item.status === Status.StatusCompleted ? 100 : 0;
  const running = item.status === Status.StatusRunning;
  const showBar = running || item.status === Status.StatusPaused || (item.status !== Status.StatusCompleted && item.completed > 0);
  const nameId = taskRowId(item.id) + "-name";
  // Two quick actions on the row; the rest are in the menu.
  const quick = actions(item).filter((a) => !a.danger).slice(0, 2);
  return (
    <li
      ref={ref}
      id={taskRowId(item.id)}
      className={`row ${item.status} ${current || checked ? "selected" : ""}`}
      aria-current={current || undefined}
      tabIndex={-1}
      onFocus={(event) => {
        if (event.target === event.currentTarget) onSelect(item.id);
      }}
      onMouseDown={(event) => {
        event.currentTarget.focus({ preventScroll: true });
        onSelect(item.id);
      }}
      onDoubleClick={() => onOpen(item)}
      onContextMenu={(e) => {
        e.preventDefault();
        onContext(item.id, e.clientX, e.clientY, e.currentTarget);
      }}
      title={name}
    >
      <label className="task-checkbox" onMouseDown={(e) => e.stopPropagation()} onDoubleClick={(e) => e.stopPropagation()}>
        <input type="checkbox" checked={checked} aria-label={t("bulk.selectTask", { name })} onChange={(e) => onCheck(item.id, e.target.checked)} />
      </label>
      <div className="row-file">
        <span className={`kind ${kind}`} aria-hidden="true">
          <Icon size={16} strokeWidth={1.8} />
        </span>
        <div className="row-main">
          <div className="row-title">
            <span className="name" id={nameId}>{name}</span>
            {item.priority === 1 && <span className="badge high">{t("priority.high")}</span>}
            {item.priority === -1 && <span className="badge low">{t("priority.low")}</span>}
          </div>
          {fileNotice ? <span className="row-note file-warning" title={fileNotice}>{fileNotice}</span> : <Note item={item} t={t} />}
        </div>
      </div>
      <div className="row-progress">
        {showBar && (
          <progress className={`bar ${item.status}`} aria-labelledby={nameId} max={100} value={known || !running ? pct : undefined} />
        )}
      </div>
      <div className="row-facts">
        <span className={`status ${item.status}`}>
          <span className="dot" />
          {converting ? t("row.converting") : t(`status.${item.status}` as "status.queued")}
        </span>
        <span className="speed">{running && bps > 0 ? speed(bps) : ""}</span>
        <span className="arrival">{arrival(item, bps, t, lang)}</span>
      </div>
      <div className="row-side">
        <span className="pct">{item.status === Status.StatusCompleted ? (known ? bytes(item.total) : "") : known ? `${Math.floor(pct)}%` : ""}</span>
        <div className="row-actions" onMouseDown={(e) => e.stopPropagation()} onDoubleClick={(e) => e.stopPropagation()}>
          {quick.map((a) => (
            <button key={a.label} className="icon ghost" title={a.label} aria-label={t("row.actionFor", { action: a.label, name })} onClick={a.onSelect}>
              {a.icon && <a.icon size={15} />}
            </button>
          ))}
          <button
            className="icon ghost"
            title={t("details.title")}
            aria-label={t("details.for", { name })}
            onClick={() => onDetails(item.id)}
          >
            <Info size={15} />
          </button>
          <button
            className="icon ghost"
            title={t("header.more")}
            aria-label={t("row.actionsFor", { name })}
            aria-haspopup="menu"
            aria-expanded={!!menuId}
            aria-controls={menuId}
            onClick={(event) => {
              const button = event.currentTarget;
              const { left, bottom } = button.getBoundingClientRect();
              onContext(item.id, event.detail === 0 ? left : event.clientX, event.detail === 0 ? bottom : event.clientY, button);
            }}
          >
            <Ellipsis size={15} />
          </button>
        </div>
      </div>
    </li>
  );
});

/** SpeedCard is a compact status line when idle and a live readout while downloading. */
function SpeedCard({ running, queued, rate, bps, history }: { running: Item[]; queued: number; rate: (id: string) => number; bps: number; history: number[] }) {
  const { t } = useI18n();
  if (running.length === 0) {
    return (
      <div className="speed-card idle">
        <span className="speed-icon">{queued > 0 ? <Hourglass size={15} /> : <Gauge size={15} />}</span>
        <div className="speed-idle-text">
          <strong>{t("speed.idle")}</strong>
          <span>{queued > 0 ? t("speed.queued", { n: queued }) : t("speed.none")}</span>
        </div>
      </div>
    );
  }
  // Everything is done when the slowest download is; that needs every size and rate.
  const secs = running.map((i) => (i.total > 0 && rate(i.id) > 0 ? (i.total - i.completed) / rate(i.id) : NaN));
  const left = secs.every(Number.isFinite) ? eta(Math.max(...secs), 1, t) : "";
  const [value, unit] = bps > 0 ? speed(bps).split(" ") : ["", ""];
  return (
    <div className="speed-card">
      <div className="speed-top">
        <span className="speed-live">
          <span className="live-dot" />
          {t("speed.downloading", { n: running.length })}
        </span>
        {queued > 0 && <span className="speed-queued">{t("speed.queued", { n: queued })}</span>}
      </div>
      {bps > 0 ? (
        <div className="speed-value">
          {value}
          <span className="speed-unit">{unit}</span>
        </div>
      ) : (
        <div className="speed-value connecting">{t("speed.connecting")}</div>
      )}
      <Sparkline values={history} />
      {left && <div className="speed-eta">{t("row.left", { t: left })}</div>}
    </div>
  );
}

/** Note is the line under a file name: what went wrong, or how much has arrived. */
function Note({ item, t }: { item: Item; t: T }) {
  if (item.recovery === Recovery.RecoveryRestartRequired) return <span className="row-note error-text">{t("reauth.restartRequired")}</span>;
  if (item.status === Status.StatusFailed) return <span className="row-note error-text">{t(`failure.title.${taskFailure(item) || "unknown"}`)}</span>;
  if (item.status === Status.StatusCompleted) return null;
  if (item.total > 0) return <span className="row-note">{t("row.of", { done: bytes(item.completed), total: bytes(item.total) })}</span>;
  if (item.completed > 0) return <span className="row-note">{bytes(item.completed)}</span>;
  return null;
}

/** arrival is the board's "arrives" column: time left while running, or when a download finished. */
function arrival(item: Item, bps: number, t: T, lang: string): string {
  if (item.status === Status.StatusRunning) {
    const left = item.total > 0 ? eta(item.total - item.completed, bps, t) : "";
    return left ? t("row.left", { t: left }) : "";
  }
  if (item.status !== Status.StatusCompleted) return "";
  const at = new Date(item.updatedAt);
  if (Number.isNaN(at.getTime())) return "";
  const sameDay = at.toDateString() === new Date().toDateString();
  return new Intl.DateTimeFormat(lang, sameDay ? { hour: "2-digit", minute: "2-digit" } : { month: "short", day: "numeric" }).format(at);
}

function Empty({ all, query, onAdd }: { all: number; query: string; onAdd: () => void }) {
  const { t } = useI18n();
  if (all > 0) {
    return (
      <div className="empty">
        <div className="empty-icon quiet">{query ? <Search size={22} /> : <Inbox size={22} />}</div>
        <p className="empty-title">{query ? t("empty.search", { q: query }) : t("empty.filtered")}</p>
      </div>
    );
  }
  return (
    <div className="empty">
      <div className="empty-icon">
        <ArrowDownToLine size={28} strokeWidth={2} />
      </div>
      <p className="empty-title">{t("empty.title")}</p>
      <p className="empty-body">{t("empty.body")}</p>
      <button className="primary" onClick={onAdd}>
        <Plus size={16} strokeWidth={2.4} />
        {t("app.newDownload")}
      </button>
    </div>
  );
}

function ContextMenu({ id, label, x, y, trigger, entries, onClose, onRemovedFocus }: {
  id: string;
  label: string;
  x: number;
  y: number;
  trigger: HTMLElement;
  entries: Action[];
  onClose: () => void;
  onRemovedFocus: () => void;
}) {
  const { element: menu, ref } = useFocusFallbackOnRemove<HTMLDivElement>(onRemovedFocus);
  const [pos, setPos] = useState({ left: x, top: y });
  useLayoutEffect(() => {
    const el = menu.current!;
    setPos({ left: Math.min(x, window.innerWidth - el.offsetWidth - 8), top: Math.min(y, window.innerHeight - el.offsetHeight - 8) });
    el.querySelector("button")?.focus({ preventScroll: true });
  }, [x, y, trigger]);
  useLayoutEffect(() => {
    // A download update can remove the focused action without moving focus.
    if (document.activeElement === document.body) {
      menu.current?.querySelector("button")?.focus({ preventScroll: true });
    }
  });
  useEffect(() => {
    const close = (e: MouseEvent) => {
      if (!(e.target instanceof Node) || menu.current?.contains(e.target)) return;
      if (trigger instanceof HTMLButtonElement && trigger.contains(e.target)) return;
      onClose();
    };
    window.addEventListener("mousedown", close);
    window.addEventListener("blur", onClose);
    return () => {
      window.removeEventListener("mousedown", close);
      window.removeEventListener("blur", onClose);
    };
  }, [trigger, onClose]);
  const dismiss = () => {
    trigger.focus({ preventScroll: true });
    onClose();
  };
  return (
    <div
      id={id}
      ref={ref}
      className="menu floating-menu"
      style={pos}
      role="menu"
      aria-label={label}
      onBlur={(event) => {
        if (event.relatedTarget && event.relatedTarget !== trigger && !event.currentTarget.contains(event.relatedTarget)) onClose();
      }}
      onKeyDown={(event) => {
        if (event.key === "Escape" || event.key === "Tab") {
          if (event.key === "Escape") event.preventDefault();
          dismiss();
          return;
        }
        if (!["ArrowUp", "ArrowDown", "Home", "End"].includes(event.key)) return;
        event.preventDefault();
        const buttons = [...event.currentTarget.querySelectorAll("button")];
        const index = buttons.indexOf(document.activeElement as HTMLButtonElement);
        const last = buttons.length - 1;
        const positions = {
          Home: 0,
          End: last,
          ArrowUp: index <= 0 ? last : index - 1,
          ArrowDown: index === last ? 0 : index + 1,
        };
        buttons[positions[event.key]]?.focus();
      }}
    >
      {entries.map((e, i) => (
        <div key={e.label} role="presentation">
          {e.danger && i > 0 && <div className="menu-sep" role="separator" />}
          <button
            role="menuitem"
            tabIndex={-1}
            className={`menu-item ${e.danger ? "destructive" : ""}`}
            onClick={() => {
              dismiss();
              e.onSelect();
            }}
          >
            {e.icon && <e.icon size={15} />}
            <span className="grow">{e.label}</span>
          </button>
        </div>
      ))}
    </div>
  );
}
