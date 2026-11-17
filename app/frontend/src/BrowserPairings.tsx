import { useEffect, useId, useRef, useState } from "react";
import { Desktop, message } from "./api";
import { type Record as Pairing, State } from "../bindings/github.com/amemiya02/nimget/internal/pairing";
import { useI18n } from "./i18n";

const states = {
  [State.$zero]: "pairing.disconnected",
  [State.Disconnected]: "pairing.disconnected",
  [State.AwaitingApproval]: "pairing.pending",
  [State.Connected]: "pairing.connected",
  [State.Denied]: "pairing.denied",
  [State.Revoked]: "pairing.revoked",
} as const;

export default function BrowserPairings() {
  const { t } = useI18n();
  const [records, setRecords] = useState<Pairing[] | null>(null);
  const [error, setError] = useState("");
  const [revision, refresh] = useState(0);

  useEffect(() => {
    let active = true;
    let timer = 0;
    async function load() {
      try {
        const next = await Desktop.ExtensionPairings();
        if (active) {
          setRecords(next);
          setError("");
        }
      } catch (err) {
        if (active) {
          setRecords(null);
          setError(message(err));
        }
      } finally {
        if (active) timer = window.setTimeout(load, 2000);
      }
    }
    void load();
    return () => {
      active = false;
      window.clearTimeout(timer);
    };
  }, [revision]);

  return (
    <>
      <p className="hint pairing-intro">{t("pairing.instructions")}</p>
      {error && <p role="alert" className="form-error">{t("pairing.disconnected")} · {error}</p>}
      {!error && !records && <p className="muted">{t("common.loading")}</p>}
      {records?.length === 0 && <p className="muted" role="status">{t("pairing.disconnected")}</p>}
      {!!records?.length && (
        <ul className="pairing-list">
          {records.map((record) => (
            <PairingRow key={record.id} record={record} onChanged={() => refresh((value) => value + 1)} />
          ))}
        </ul>
      )}
    </>
  );
}

function PairingRow({ record, onChanged }: { record: Pairing; onChanged: () => void }) {
  const { t } = useI18n();
  const codeID = useId();
  const errorID = useId();
  const row = useRef<HTMLLIElement>(null);
  const [code, setCode] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  async function apply(action: () => Promise<void>) {
    if (busy) return;
    setBusy(true);
    setError("");
    try {
      await action();
      row.current?.focus();
      setCode("");
      onChanged();
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <li className="set-group pairing-row" ref={row} tabIndex={-1}>
      <div className="set-row">
        <div className="set-text">
          <span className="set-label">{record.browser === "edge" ? "Microsoft Edge" : "Google Chrome"}</span>
          <span className="hint">{t("pairing.request", { id: record.id.slice(-6) })}</span>
        </div>
        <span role="status" className="pairing-status">{t(states[record.status])}</span>
        {record.status === State.Connected && (
          <button type="button" disabled={busy} onClick={() => apply(() => Desktop.RevokeExtensionPairing(record.id))}>
            {t("pairing.revoke")}
          </button>
        )}
      </div>
      {record.status === State.AwaitingApproval && (
        <form className="pairing-form" onSubmit={(event) => {
          event.preventDefault();
          void apply(() => Desktop.ApproveExtensionPairing(record.id, code));
        }}>
          <label htmlFor={codeID}>{t("pairing.code")}</label>
          <div className="pairing-actions">
            <input
              id={codeID}
              value={code}
              onChange={(event) => { setCode(event.target.value); setError(""); }}
              autoComplete="off"
              autoCapitalize="characters"
              spellCheck={false}
              maxLength={8}
              pattern="[A-Fa-f0-9]{8}"
              required
              aria-describedby={error ? errorID : undefined}
              aria-invalid={error ? true : undefined}
            />
            <button type="button" disabled={busy} onClick={() => apply(() => Desktop.DenyExtensionPairing(record.id))}>
              {t("pairing.deny")}
            </button>
            <button type="submit" className="primary" disabled={busy}>{t("pairing.approve")}</button>
          </div>
        </form>
      )}
      <p id={errorID} role="alert" className="form-error">{error}</p>
    </li>
  );
}
