import { useId, useState, type FormEvent } from "react";
import { KeyRound, Moon, Power, RotateCw, Trash2 } from "lucide-react";
import { Desktop, Item, Status, message } from "./api";
import { baseName } from "./format";
import { useI18n } from "./i18n";
import { Modal } from "./ui";

export function DeleteDialog({ item, onClose, onError }: { item: Item; onClose: () => void; onError: (e: unknown) => void }) {
  const { t } = useI18n();
  const [files, setFiles] = useState(false);
  const [busy, setBusy] = useState(false);
  const confirm = async () => {
    setBusy(true);
    try {
      // The status may have changed since the dialog opened; pausing a
      // stopped download is a harmless conflict.
      await Desktop.Pause(item.id).catch(() => {});
      // A paused runner may still be checkpointing; retry briefly.
      for (let i = 0; ; i++) {
        try {
          await Desktop.Delete(item.id, files);
          break;
        } catch (err) {
          if (i >= 20 || !/active/.test(message(err))) throw err;
          await new Promise((r) => setTimeout(r, 150));
        }
      }
    } catch (err) {
      onError(err);
    }
    onClose();
  };
  return (
    <Modal
      icon={Trash2}
      tone="danger"
      size="small"
      title={t("delete.title")}
      onClose={onClose}
      footer={
        <>
          <button onClick={onClose}>{t("common.cancel")}</button>
          <button className="danger" disabled={busy} onClick={confirm} autoFocus>
            <Trash2 size={14} />
            {t("delete.confirm")}
          </button>
        </>
      }
    >
      <p className="filename">{baseName(item.destination)}</p>
      <label className="check">
        <input type="checkbox" checked={files} onChange={(e) => setFiles(e.target.checked)} />
        {item.status === Status.StatusCompleted ? t("delete.file") : t("delete.partial")}
      </label>
    </Modal>
  );
}

export function ReauthDialog({ item, onClose }: { item: Item; onClose: () => void }) {
  const { t } = useI18n();
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const id = useId();
  const restartRequired = item.recovery === "restart-required";

  const run = async (action: () => Promise<void>) => {
    setBusy(true);
    setError("");
    try {
      await action();
      onClose();
    } catch (err) {
      setError(message(err));
      setBusy(false);
    }
  };

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    void run(() => Desktop.Reauthenticate(item.id, text));
  };

  return (
    <Modal
      icon={KeyRound}
      tone="warn"
      title={t("reauth.title")}
      onClose={onClose}
      footer={
        <>
          <button type="button" onClick={onClose}>{t("common.cancel")}</button>
          {restartRequired && (
            <button type="button" className="danger" disabled={busy} onClick={() => void run(() => Desktop.Restart(item.id))}>
              {t("reauth.restart")}
            </button>
          )}
          <button type="submit" form={id + "-form"} className="primary" disabled={busy}>
            <RotateCw size={14} />
            {t("reauth.submit")}
          </button>
        </>
      }
    >
      {restartRequired && <p>{t("reauth.restartBody")}</p>}
      <p id={id + "-help"} className="muted">{t("reauth.body", { name: baseName(item.destination) })}</p>
      <form id={id + "-form"} method="post" onSubmit={submit}>
        <label className="field" htmlFor={id}>
          <span className="field-label">{t("reauth.source")}</span>
          <textarea
            id={id}
            name="source"
            className="mono"
            rows={5}
            value={text}
            onChange={(e) => {
              setText(e.target.value);
              setError("");
            }}
            aria-describedby={id + "-help"}
            autoFocus
            required
            spellCheck={false}
            autoCorrect="off"
            autoCapitalize="none"
          />
        </label>
        <p className="error-text" role="status">{error}</p>
      </form>
    </Modal>
  );
}

export function CountdownDialog({ action, left, onCancel }: { action: string; left: number; onCancel: () => void }) {
  const { t } = useI18n();
  const shutdown = action === "shutdown";
  return (
    <Modal
      icon={shutdown ? Power : Moon}
      tone="warn"
      size="small"
      title={t("countdown.title")}
      onClose={onCancel}
      footer={
        <button className="primary" onClick={onCancel} autoFocus>
          {t("common.cancel")}
        </button>
      }
    >
      <div className="countdown">
        <span className="countdown-num">{left}</span>
        <p className="muted">{shutdown ? t("countdown.shutdown", { n: left }) : t("countdown.sleep", { n: left })}</p>
      </div>
    </Modal>
  );
}
