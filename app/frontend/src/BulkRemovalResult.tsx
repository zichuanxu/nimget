import { useEffect, useMemo, useRef, useState } from "react";
import { ChevronRight, CircleAlert, CircleCheck, X } from "lucide-react";
import { TaskAction } from "./api";
import type { BulkActionReport } from "./BulkTaskActions";
import { useI18n } from "./i18n";
import { Modal } from "./ui";

const successDuration = 8000;

export default function BulkRemovalResult({
  report,
  detailsOpen,
  modalOpen,
  onOpenDetails,
  onCloseDetails,
  focusFallback,
}: {
  report: BulkActionReport | null;
  detailsOpen: boolean;
  modalOpen: boolean;
  onOpenDetails: () => void;
  onCloseDetails: () => void;
  focusFallback: () => void;
}) {
  const { t } = useI18n();
  const [dismissed, setDismissed] = useState<BulkActionReport | null>(null);
  const [hovered, setHovered] = useState(false);
  const [focused, setFocused] = useState(false);
  const remaining = useRef(successDuration);
  const groups = useMemo(() => {
    const grouped: Record<BulkActionReport["outcomes"][number]["status"], BulkActionReport["outcomes"]> = {
      succeeded: [], failed: [], skipped: [], unknown: [],
    };
    for (const outcome of report?.outcomes ?? []) grouped[outcome.status].push(outcome);
    return grouped;
  }, [report]);

  const visible = !!report && report !== dismissed;
  const { succeeded, failed, skipped, unknown } = groups;
  const incomplete = failed.length + skipped.length + unknown.length;
  const success = incomplete === 0 && !report?.error;
  const paused = hovered || focused || modalOpen;

  useEffect(() => {
    remaining.current = successDuration;
  }, [report]);

  useEffect(() => {
    if (!visible || !success || paused) return;
    const started = performance.now();
    const timer = window.setTimeout(() => setDismissed(report), remaining.current);
    return () => {
      window.clearTimeout(timer);
      remaining.current = Math.max(0, remaining.current - (performance.now() - started));
    };
  }, [paused, report, success, visible]);

  const counts = [
    succeeded.length && t("bulk.countSucceeded", { n: succeeded.length }),
    failed.length && t("bulk.countFailed", { n: failed.length }),
    skipped.length && t("bulk.countSkipped", { n: skipped.length }),
    unknown.length && t("bulk.countUnknown", { n: unknown.length }),
  ].filter(Boolean).join(" · ");
  const title = success
    ? t(succeeded.length === 1 ? "bulk.removedOne" : "bulk.removed", { n: succeeded.length })
    : t(incomplete === 1 ? "bulk.incompleteOne" : "bulk.incomplete", { n: incomplete });
  const summary = success
    ? t(report?.action === TaskAction.TaskActionRemoveFiles ? "bulk.cleanupComplete" : "bulk.filesKept")
    : counts;
  const Icon = success ? CircleCheck : CircleAlert;

  return (
    <>
      <span className="bulk-removal-announcement" role="status">
        {visible && <>{title}. {summary}</>}
      </span>
      {visible && (
        <section
          className={`card bulk-removal-toast ${success ? "success" : "warn"}`}
          aria-label={t("bulk.removalNotice")}
          data-obscured={modalOpen || undefined}
          onMouseEnter={() => setHovered(true)}
          onMouseLeave={() => setHovered(false)}
          onFocus={() => setFocused(true)}
          onBlur={(event) => {
            if (!event.currentTarget.contains(event.relatedTarget)) setFocused(false);
          }}
        >
          <span className="card-icon"><Icon size={16} aria-hidden="true" /></span>
          <div className="bulk-removal-copy">
            <strong>{title}</strong>
            <span>{summary}</span>
          </div>
          <div className="bulk-removal-actions">
            <button type="button" className="ghost small bulk-removal-details" aria-haspopup="dialog" onClick={onOpenDetails}>
              {t("bulk.details")}
            </button>
            <button
              type="button"
              className="icon ghost"
              aria-label={t("bulk.dismissResults")}
              title={t("bulk.dismissResults")}
              onClick={(event) => {
                if (event.currentTarget.parentElement?.contains(document.activeElement)) focusFallback();
                setHovered(false);
                setFocused(false);
                setDismissed(report);
              }}
            >
              <X size={14} aria-hidden="true" />
            </button>
          </div>
        </section>
      )}
      {report && detailsOpen && (
        <Modal
          icon={Icon}
          tone={success ? "success" : "warn"}
          title={t("bulk.removalResults")}
          description={counts}
          onClose={onCloseDetails}
          focusFallback={focusFallback}
        >
          {report.error && <p className="bulk-removal-reason error-text">{report.error}</p>}
          {!success && incomplete > 0 && (
            <OutcomeList outcomes={[...failed, ...skipped, ...unknown]} />
          )}
          {success && <OutcomeList outcomes={succeeded} />}
          {!success && succeeded.length > 0 && (
            <details className="bulk-removal-succeeded">
              <summary>
                <ChevronRight size={14} aria-hidden="true" />
                {t(succeeded.length === 1 ? "bulk.showSucceededOne" : "bulk.showSucceeded", { n: succeeded.length })}
              </summary>
              <OutcomeList outcomes={succeeded} />
            </details>
          )}
        </Modal>
      )}
    </>
  );
}

function OutcomeList({ outcomes }: { outcomes: BulkActionReport["outcomes"] }) {
  const { t } = useI18n();
  return (
    <ul className="bulk-removal-outcomes" role="list" aria-label={t("bulk.taskResults")}>
      {outcomes.map((outcome) => {
        const reason = outcome.fileCleanupError
          ? t("bulk.recordRemovedWithError", { error: outcome.fileCleanupError })
          : outcome.status === "skipped" ? t("bulk.removalSkipped") : outcome.error;
        return (
          <li key={outcome.id}>
            <div className="bulk-removal-outcome">
              <strong title={outcome.destination}>{outcome.label}</strong>
              <span data-status={outcome.status}>
                {outcome.fileCleanupError ? t("bulk.cleanupFailed") : t(`bulk.${outcome.status}`)}
              </span>
            </div>
            {reason && <p className="bulk-removal-reason">{reason}</p>}
          </li>
        );
      })}
    </ul>
  );
}
