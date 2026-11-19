import { useId, useLayoutEffect, useRef, useState } from "react";
import { Pause, Play, RotateCw, Trash2, X } from "lucide-react";
import { Desktop, Item, Recovery, Status, TaskAction, message } from "./api";
import { taskName } from "./format";
import { useI18n } from "./i18n";
import { Modal } from "./ui";

const { TaskActionPause, TaskActionResume, TaskActionRetry, TaskActionRemoveRecords, TaskActionRemoveFiles } = TaskAction;

const actions = [
  { action: TaskActionPause, label: "row.pause", icon: Pause },
  { action: TaskActionResume, label: "row.resume", icon: Play },
  { action: TaskActionRetry, label: "row.retry", icon: RotateCw },
  { action: TaskActionRemoveRecords, label: "bulk.remove", icon: Trash2 },
] as const;

function eligible(item: Item, action: TaskAction): boolean {
  switch (action) {
    case TaskActionPause:
      return item.status === Status.StatusQueued || item.status === Status.StatusRunning;
    case TaskActionResume:
      return item.status === Status.StatusPaused && item.recovery !== Recovery.RecoveryRestartRequired;
    case TaskActionRetry:
      return item.status === Status.StatusFailed && item.recovery !== Recovery.RecoveryRestartRequired;
    case TaskActionRemoveRecords:
    case TaskActionRemoveFiles:
      return item.status !== Status.StatusRunning;
    default:
      return false;
  }
}

type Outcome = {
  id: string;
  destination: string;
  label: string;
  status: "succeeded" | "failed" | "skipped" | "unknown";
  error?: string;
  fileCleanupError?: string;
};

export type BulkActionReport = { action: TaskAction; outcomes: Outcome[]; error?: string };

export default function BulkTaskActions({
  visibleCount,
  multiplePages,
  selected,
  disabled,
  onSelectAll,
  onConfirm,
  onRemovalResult,
  confirming,
  refresh,
}: {
  visibleCount: number;
  multiplePages: boolean;
  selected: Item[];
  disabled: boolean;
  onSelectAll: (checked: boolean) => void;
  onConfirm: (items: Item[] | null) => void;
  onRemovalResult: (report: BulkActionReport) => void;
  confirming: Item[] | null;
  refresh: () => Promise<void>;
}) {
  const { t } = useI18n();
  const heading = useId();
  const selectAll = useRef<HTMLInputElement>(null);
  const [busy, setBusy] = useState(false);
  const [report, setReport] = useState<BulkActionReport | null>(null);
  const picked = selected.length;
  const allPicked = picked > 0 && picked === visibleCount;

  useLayoutEffect(() => {
    if (selectAll.current) selectAll.current.indeterminate = picked > 0 && !allPicked;
  }, [picked, allPicked]);

  const run = async (
    action: TaskAction,
    targets: Item[],
    ids = targets.filter((item) => eligible(item, action)).map((item) => item.id),
  ) => {
    if (busy || disabled) return;
    if (!ids.length) return;
    const applicable = new Set(ids);
    setBusy(true);
    setReport(null);
    onConfirm(null);

    const names = new Map<string, number>();
    for (const item of targets) {
      const name = taskName(item);
      names.set(name, (names.get(name) ?? 0) + 1);
    }
    const outcomes: Outcome[] = targets.map((item) => {
      const name = taskName(item);
      return {
        id: item.id,
        destination: item.destination,
        label: names.get(name) === 1 ? name : item.destination,
        status: applicable.has(item.id) ? "unknown" : "skipped",
      };
    });
    let error: string | undefined;
    try {
      const results = await Desktop.ApplyTaskAction(action, ids);
      const byID = new Map(results.map((result) => [result.id, result]));
      for (const outcome of outcomes) {
        const result = byID.get(outcome.id);
        if (result) {
          outcome.status = result.error ? "failed" : "succeeded";
          outcome.error = result.error;
          outcome.fileCleanupError = result.fileCleanupError;
        }
      }
    } catch (err) {
      error = message(err);
    } finally {
      await refresh();
      const result = { action, outcomes, error };
      if (action === TaskActionRemoveRecords || action === TaskActionRemoveFiles) onRemovalResult(result);
      else setReport(result);
      setBusy(false);
    }
  };

  const summary = report?.outcomes.reduce(
    (counts, outcome) => {
      counts[outcome.status]++;
      return counts;
    },
    { succeeded: 0, failed: 0, skipped: 0, unknown: 0 },
  );
  const actionLabel = report && actions.find((action) => action.action === report.action)?.label;
  const removable = confirming ? new Set(selected.filter((item) => eligible(item, TaskActionRemoveRecords)).map((item) => item.id)) : null;
  const confirmedIDs = confirming?.filter((item) => eligible(item, TaskActionRemoveRecords) && removable?.has(item.id)).map((item) => item.id) ?? [];

  return (
    <>
      <div className="task-selection" role="group" aria-label={t("bulk.group")}>
        <label className="select-visible">
          <input
            type="checkbox"
            ref={selectAll}
            checked={allPicked}
            disabled={visibleCount === 0}
            onChange={(event) => onSelectAll(event.target.checked)}
          />
          {t(multiplePages ? "bulk.selectMatching" : "bulk.selectAll")}
        </label>
        <span className="selection-count" role="status">{picked > 0 && t("bulk.selected", { n: picked })}</span>
        {picked > 0 && (
          <div className="selection-actions">
            {actions.map(({ action, label, icon: Icon }) => {
              const count = selected.filter((item) => eligible(item, action)).length;
              return (
                <button
                  key={action}
                  className="small"
                  disabled={count === 0 || disabled}
                  aria-disabled={busy || undefined}
                  onClick={() => {
                    if (busy) return;
                    if (action === TaskActionRemoveRecords) onConfirm(selected);
                    else void run(action, selected);
                  }}
                >
                  <Icon size={14} aria-hidden="true" />
                  {t("bulk.actionCount", { action: t(label), n: count, total: picked })}
                </button>
              );
            })}
          </div>
        )}
        {busy && <span role="status">{t("bulk.working")}</span>}
      </div>
      {report && summary && (
        <section className="bulk-results" aria-labelledby={heading}>
          <div className="bulk-results-head">
            <h2 id={heading}>{t("bulk.results")}</h2>
            <p role="status">
              {actionLabel && <>{t(actionLabel)} · </>}{t("bulk.summary", summary)}
              {summary.unknown > 0 && <> {t("bulk.summaryUnknown", { n: summary.unknown })}</>}
            </p>
            <button className="icon ghost tiny" aria-label={t("bulk.dismissResults")} onClick={() => setReport(null)}><X size={14} /></button>
          </div>
          {report.error && <p className="error-text">{report.error}</p>}
          <ul tabIndex={0} aria-label={t("bulk.taskResults")}>
            {report.outcomes.map((outcome) => (
              <li key={outcome.id}>
                <strong title={outcome.destination}>{outcome.label}</strong>
                <span className={outcome.status === "failed" ? "error-text" : ""}>
                  {t(`bulk.${outcome.status}`)}{outcome.error && <> · {outcome.error}</>}
                </span>
              </li>
            ))}
          </ul>
        </section>
      )}
      {confirming && (
        <Modal
          title={t("bulk.confirm")}
          icon={Trash2}
          tone="danger"
          onClose={() => onConfirm(null)}
          footer={
            <div className="bulk-confirm-actions">
              <button data-initial-focus onClick={() => onConfirm(null)}>{t("common.cancel")}</button>
              <button onClick={() => void run(TaskActionRemoveRecords, confirming, confirmedIDs)} disabled={disabled || !confirmedIDs.length}>{t("bulk.records")}</button>
              <button className="danger" onClick={() => void run(TaskActionRemoveFiles, confirming, confirmedIDs)} disabled={disabled || !confirmedIDs.length}>{t("bulk.files")}</button>
            </div>
          }
        >
          <p>{t("bulk.confirmCount", { n: confirmedIDs.length, total: confirming.length })}</p>
          <p>{t("bulk.confirmHelp")}</p>
        </Modal>
      )}
    </>
  );
}
