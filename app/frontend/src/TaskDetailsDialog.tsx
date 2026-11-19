import { useId, useState, type FormEvent } from "react";
import { Info } from "lucide-react";
import { Desktop, Failure, Item, Patch, Phase, Recovery, SourceKind, Status } from "./api";
import { bytes, parseSpeed, speedText, taskStatus } from "./format";
import { useI18n } from "./i18n";
import { taskFailure } from "./taskFailure";
import { Modal } from "./ui";

export default function TaskDetailsDialog({ item, fileNotice, onClose, onRecover, focusFallback }: {
  item: Item;
  fileNotice?: string;
  onClose: () => void;
  onRecover: () => void;
  focusFallback: () => void;
}) {
  const { t } = useI18n();
  const id = useId();
  const [priority, setPriority] = useState(item.priority);
  const [connections, setConnections] = useState(item.connections ? String(item.connections) : "");
  const [limit, setLimit] = useState(speedText(item.speedLimit ?? 0));
  const [directConsent, setDirectConsent] = useState<boolean>();
  const [changed, setChanged] = useState({ priority: false, connections: false, speedLimit: false });
  const [invalid, setInvalid] = useState<"connections" | "speedLimit" | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const failure = taskFailure(item);
  const restartRequired = item.recovery === Recovery.RecoveryRestartRequired;
  const failed = item.status === Status.StatusFailed;
  const magnet = item.sourceKind === SourceKind.SourceMagnet;
  const showDirectConsent = magnet && item.status !== Status.StatusCompleted && !item.duplicateOf;
  const consentChanged = showDirectConsent && directConsent !== undefined;

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (busy) return;
    const form = event.currentTarget;
    setError("");
    setInvalid(null);
    const patch = new Patch();
    if (changed.priority) patch.priority = priority;
    if (consentChanged) patch.directConnectionAllowed = directConsent;
    if (changed.connections) {
      const value = connections.trim();
      const count = Number(value);
      if (value && (!/^\d+$/.test(value) || count < 1 || count > 32)) {
        setInvalid("connections");
        setError(t("details.connectionsError"));
        form.querySelector<HTMLInputElement>("[name=connections]")?.focus();
        return;
      }
      patch.connections = count;
    }
    if (changed.speedLimit) {
      try {
        const speed = parseSpeed(limit);
        if (!Number.isSafeInteger(speed)) throw new RangeError();
        patch.speedLimit = speed;
      } catch {
        setInvalid("speedLimit");
        setError(t("details.speedError"));
        form.querySelector<HTMLInputElement>("[name=speedLimit]")?.focus();
        return;
      }
    }
    setBusy(true);
    try {
      await Desktop.Update(item.id, patch);
      onClose();
    } catch {
      setError(t("details.saveError"));
      setBusy(false);
    }
  };

  const describe = (field: "connections" | "speedLimit") => `${id}-${field}-hint${invalid === field ? ` ${id}-error` : ""}`;
  const done = bytes(item.completed);
  const retry = async () => {
    setBusy(true);
    setError("");
    setInvalid(null);
    try {
      await Desktop.Retry(item.id);
      onClose();
    } catch {
      setError(t("details.retryError"));
      setBusy(false);
    }
  };

  return (
    <Modal
      icon={Info}
      title={t("details.title")}
      onClose={() => !busy && onClose()}
      focusFallback={focusFallback}
      footer={
        <>
          <button type="button" disabled={busy} onClick={onClose}>{t("common.close")}</button>
          <button type="submit" className="primary" form={`${id}-form`} disabled={busy || !(consentChanged || Object.values(changed).some(Boolean))}>
            {t("common.save")}
          </button>
        </>
      }
    >
      <dl className="task-facts">
        <dt>{t("details.destination")}</dt>
        <dd>{item.destination || item.directory}{fileNotice && <div className="file-warning">{fileNotice}</div>}</dd>
        <dt>{t("details.status")}</dt>
        <dd>{taskStatus(item, t)}</dd>
        <dt>{t("details.progress")}</dt>
        <dd>{item.total > 0 ? t("row.of", { done, total: bytes(item.total) }) : done}</dd>
      </dl>
      {restartRequired ? (
        <div className="task-recovery">
          <p>{t("reauth.restartRequired")}</p>
          <button type="button" disabled={busy} onClick={onRecover}>{t("row.recover")}</button>
        </div>
      ) : failed ? (
        <div className="task-recovery">
          <strong className="error-text">{t(`failure.title.${failure || "unknown"}`)}</strong>
          <p>{magnet && item.error ? item.error : t(`failure.help.${failure || "unknown"}`)}</p>
          <div className="task-recovery-actions">
            {failure === Failure.FailureAuthorization && (
              <button type="button" disabled={busy} onClick={onRecover}>{t("row.reauth")}</button>
            )}
            {!item.duplicateOf && <button type="button" disabled={busy} onClick={() => void retry()}>{t("row.retry")}</button>}
          </div>
        </div>
      ) : item.recovery === Recovery.RecoveryValidateSource ? (
        <p className="hint">{t("details.validating")}</p>
      ) : null}
      <form id={`${id}-form`} className="task-options" onSubmit={submit}>
        <div className="field">
          <label className="field-label" htmlFor={`${id}-priority`}>{t("add.priority")}</label>
          <select id={`${id}-priority`} name="priority" aria-describedby={`${id}-priority-hint`} value={priority} disabled={busy} data-initial-focus onChange={(event) => {
            setPriority(Number(event.target.value));
            setChanged({ ...changed, priority: true });
          }}>
            <option value={1}>{t("priority.high")}</option>
            <option value={0}>{t("priority.normal")}</option>
            <option value={-1}>{t("priority.low")}</option>
          </select>
          <span className="hint" id={`${id}-priority-hint`}>{t("details.priorityHint")}</span>
        </div>
        {showDirectConsent && (
          <div className="field">
            <label className="check task-direct-consent" htmlFor={`${id}-direct-consent`}>
              <input
                id={`${id}-direct-consent`}
                name="directConnectionAllowed"
                type="checkbox"
                checked={directConsent ?? !!item.directConnectionAllowed}
                disabled={busy}
                aria-describedby={`${id}-direct-consent-hint`}
                onChange={(event) => setDirectConsent(event.target.checked)}
              />
              {t("details.directConsent")}
            </label>
            <span className="hint" id={`${id}-direct-consent-hint`}>
              {t(item.phase === Phase.PhaseProxyBlocked ? "details.directConsentBlocked" : "details.directConsentHint")}
            </span>
          </div>
        )}
        {!magnet && <><div className="field">
          <label className="field-label" htmlFor={`${id}-connections`}>{t("add.connections")}</label>
          <input id={`${id}-connections`} name="connections" inputMode="numeric" placeholder={t("add.default")} value={connections} disabled={busy} aria-describedby={describe("connections")} aria-invalid={invalid === "connections"} onChange={(event) => {
            setConnections(event.target.value);
            setChanged({ ...changed, connections: true });
            if (invalid === "connections") {
              setInvalid(null);
              setError("");
            }
          }} />
          <span className="hint" id={`${id}-connections-hint`}>{t("details.connectionsHint")}</span>
        </div>
        <div className="field">
          <label className="field-label" htmlFor={`${id}-speedLimit`}>{t("add.speedLimit")}</label>
          <input id={`${id}-speedLimit`} name="speedLimit" placeholder={t("details.unlimited")} value={limit} disabled={busy} aria-describedby={describe("speedLimit")} aria-invalid={invalid === "speedLimit"} onChange={(event) => {
            setLimit(event.target.value);
            setChanged({ ...changed, speedLimit: true });
            if (invalid === "speedLimit") {
              setInvalid(null);
              setError("");
            }
          }} />
          <span className="hint" id={`${id}-speedLimit-hint`}>{t("details.speedHint")}</span>
        </div>
        </>}
        {error && <p className="error-text" id={`${id}-error`} role="alert">{error}</p>}
      </form>
    </Modal>
  );
}
