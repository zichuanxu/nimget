import { useEffect, useId, useRef, useState } from "react";
import { ArrowDownToLine, ChevronRight, CircleAlert, Cookie, Folder, Layers, Link2, X } from "lucide-react";
import { Desktop, Request, message } from "./api";
import { baseName, joinPath, looksLikeCurl, looksLikeMagnet, looksLikeURL, parseSpeed } from "./format";
import { useI18n } from "./i18n";
import { Field, Modal, Segmented } from "./ui";

export type AddPrefill = { text?: string };

type BatchEntry = { url: string } & (
  { status: "pending" } | { status: "accepted"; id: string } | { status: "rejected"; error: string }
);

function originOf(url: string): string {
  try {
    const parsed = new URL(url);
    return parsed.protocol === "http:" || parsed.protocol === "https:" ? parsed.origin : "";
  } catch {
    return "";
  }
}

type Props = {
  prefill: AddPrefill | null;
  hasNewPrefill: boolean;
  defaultDirectory: string;
  onClose: (keepDraft: boolean) => void;
  onStartNew: () => void;
  onError: (err: unknown) => void;
};

/** AddDialog queues one download per URL line, or one from a cURL command. */
export default function AddDialog({ prefill, hasNewPrefill, defaultDirectory, onClose, onStartNew, onError }: Props) {
  const { t } = useI18n();
  const [text, setText] = useState(prefill?.text ?? "");
  const [headers, setHeaders] = useState<Record<string, string>>({});
  // Reuse imported headers only at their original HTTP security origin.
  const [headerOrigin, setHeaderOrigin] = useState("");
  const [directory, setDirectory] = useState("");
  const [fileName, setFileName] = useState("");
  const [priority, setPriority] = useState(0);
  const [connections, setConnections] = useState("");
  const [limit, setLimit] = useState("");
  const [checksum, setChecksum] = useState("");
  const [showOptions, setShowOptions] = useState(false);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const submitting = useRef(false);
  const [batch, setBatch] = useState<BatchEntry[] | null>(null);
  const resultId = useId();
  const resultStatus = useRef<HTMLParagraphElement>(null);
  const showResults = prefill !== null && batch !== null;

  useEffect(() => {
    if (showResults && !busy) resultStatus.current?.focus();
  }, [showResults, busy]);

  // A pasted cURL command becomes a URL plus its headers.
  const isCurl = looksLikeCurl(text);
  useEffect(() => {
    if (!isCurl) return;
    let current = true;
    Desktop.ParseCurl(text)
      .then((req) => {
        if (!current) return;
        setText(req.url);
        setHeaders((req.headers as Record<string, string>) ?? {});
        setHeaderOrigin(originOf(req.url));
        setError("");
      })
      .catch(() => current && setError(t("add.curlInvalid")));
    return () => {
      current = false;
    };
  }, [text, isCurl, t]);

  useEffect(() => {
    Desktop.Settings()
      .then((s) => {
        if (s.desktop.askDirectory) pick();
      })
      .catch(() => {});
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const lines = text.split(/\r?\n/).map((l) => l.trim()).filter(Boolean);
  const pattern = !isCurl && lines.length === 1 && !looksLikeMagnet(lines[0]) && /\[[^\]]+-[^\]]+\]|\{[^}]*,[^}]*\}/.test(lines[0]) ? lines[0] : "";
  const [expansion, setExpansion] = useState<{ pattern: string; urls?: string[]; error?: string }>();
  useEffect(() => {
    setExpansion(undefined);
    if (!pattern) return;
    let current = true;
    Desktop.ExpandBatch(pattern)
      .then((list) => current && setExpansion({ pattern, urls: list ?? [] }))
      .catch((err) => current && setExpansion({ pattern, error: message(err) }));
    return () => {
      current = false;
    };
  }, [pattern]);
  const expanded = expansion?.pattern === pattern ? expansion.urls : undefined;
  const batchError = expansion?.pattern === pattern ? expansion.error : undefined;
  const urls = expanded ?? lines;
  const pendingURLs = batch?.filter((entry) => entry.status !== "accepted").map((entry) => entry.url) ?? urls;
  const magnetsOnly = pendingURLs.length > 0 && pendingURLs.every(looksLikeMagnet);
  const isBatch = batch !== null || urls.length > 1;
  const accepted = batch?.filter((entry) => entry.status === "accepted").length ?? 0;
  const rejected = batch?.filter((entry) => entry.status === "rejected").length ?? 0;
  const valid = batch !== null
    ? rejected > 0 && batch.every((entry) => entry.status !== "rejected" || entry.url.trim())
    : urls.length > 0 && !isCurl && (!pattern || !!expanded) && (isBatch || looksLikeURL(urls[0]) || looksLikeMagnet(urls[0]));

  function close() {
    if (submitting.current && batch === null) return;
    onClose(batch !== null && (submitting.current || rejected > 0));
  }

  async function pick() {
    try {
      const dir = await Desktop.PickDirectory();
      if (dir) setDirectory(dir);
    } catch (err) {
      setError(message(err));
    }
  }

  async function submit() {
    if (!valid || submitting.current) return;
    submitting.current = true;
    setBusy(true);
    setError("");
    try {
      function requestFor(url: string) {
        if (looksLikeMagnet(url)) return new Request({ url, priority, directory: directory || undefined });
        const req = new Request({
          url, priority,
          connections: connections ? parseInt(connections, 10) : 0,
          speedLimit: parseSpeed(limit),
          checksum: checksum.trim(),
        });
        if (Object.keys(headers).length && headerOrigin && originOf(url) === headerOrigin) req.headers = headers;
        if (fileName.trim() && !isBatch) {
          req.destination = joinPath(directory || defaultDirectory, fileName.trim());
        } else if (directory) {
          req.directory = directory;
        }
        return req;
      }
      if (isBatch) {
        const entries: BatchEntry[] = batch ?? urls.map((url) => ({ url, status: "pending" }));
        setBatch(entries);
        const results: BatchEntry[] = [];
        for (const entry of entries) {
          if (entry.status === "accepted") {
            results.push(entry);
            continue;
          }
          const url = entry.url.trim();
          try {
            const item = await Desktop.Add(requestFor(url));
            results.push({ url, status: "accepted", id: item.id });
          } catch (err) {
            results.push({ url, status: "rejected", error: message(err) });
          }
        }
        setBatch(results);
      } else {
        await Desktop.Add(requestFor(urls[0]));
        onClose(false);
      }
    } catch (err) {
      setError(message(err));
      onError(err);
    } finally {
      submitting.current = false;
      setBusy(false);
    }
  }

  const headerCount = Object.keys(headers).length;
  const otherOrigins = headerCount > 0 && pendingURLs.some((url) => originOf(url) !== headerOrigin);
  const optionsSet = priority !== 0 || (!magnetsOnly && (!!connections || !!limit || !!checksum));
  const finished = batch !== null && !busy && rejected === 0;
  if (!prefill) return null;

  return (
    <Modal
      icon={ArrowDownToLine}
      title={t("add.title")}
      onClose={close}
      footer={
        <>
          {batch && !busy && (
            <button type="button" className="ghost" onClick={onStartNew}>
              {t(hasNewPrefill ? "add.startNewWithLink" : "add.startNew")}
            </button>
          )}
          <button type="button" disabled={busy && !batch} onClick={close}>
            {batch ? t("common.close") : t("common.cancel")}
          </button>
          {!finished && (
            <button type="button" className="primary" disabled={!valid || busy} onClick={submit}>
              <ArrowDownToLine size={14} />
              {batch ? t("add.retryRejected") : isBatch ? t("add.submitMany", { n: urls.length }) : t("add.submit")}
            </button>
          )}
        </>
      }
    >
      {batch ? (
        <>
          <p role="status" tabIndex={-1} ref={resultStatus}>
            {busy ? t("add.submitting") : t("add.batchResults", { accepted, rejected })}
          </p>
          <ol className="batch-results">
            {batch.map((entry, index) => (
              <li key={index}>
                {entry.status === "accepted" ? (
                  <>
                    <strong>{t("add.accepted")}</strong>
                    <div className="batch-url">{entry.url}</div>
                    <small className="batch-task">{t("add.acceptedTask", { id: entry.id })}</small>
                  </>
                ) : entry.status === "rejected" ? (
                  <>
                    <Field label={t("add.rejectedLink", { n: index + 1 })}>
                      <input
                        type="text"
                        value={entry.url}
                        disabled={busy}
                        aria-describedby={`${resultId}-error-${index}`}
                        spellCheck={false}
                        onChange={(event) => {
                          const url = event.target.value;
                          setBatch((previous) => previous?.map((result, i) => i === index ? { ...result, url } : result) ?? null);
                        }}
                      />
                    </Field>
                    <p className="form-error" id={`${resultId}-error-${index}`}>{entry.error}</p>
                  </>
                ) : (
                  <div className="batch-url">{entry.url}</div>
                )}
              </li>
            ))}
          </ol>
        </>
      ) : (
        <Field label={t("add.urls")} hint={t("add.urlsHint")}>
          <div className="input-icon top">
            <Link2 size={15} />
            <textarea
              className="urls"
              rows={3}
              value={text}
              onChange={(e) => {
                setText(e.target.value);
                if (error) setError("");
              }}
              onKeyDown={(e) => {
                if (e.key === "Enter" && (e.metaKey || e.ctrlKey) && !e.nativeEvent.isComposing) {
                  e.preventDefault();
                  submit();
                }
              }}
              placeholder="https://example.com/file.zip"
              data-initial-focus
              spellCheck={false}
            />
          </div>
        </Field>
      )}

      {!batch && batchError && (
        <div className="chip danger">
          <CircleAlert size={14} />
          {t("add.batchError", { error: batchError })}
        </div>
      )}
      {!batch && expanded && expanded.length > 0 && (
        <div className="chip">
          <Layers size={14} />
          <span className="ellipsis">{t("add.batch", { n: expanded.length, first: baseName(expanded[0]), last: baseName(expanded[expanded.length - 1]) })}</span>
        </div>
      )}
      {headerCount > 0 && !magnetsOnly && (
        <div className="chip">
          <Cookie size={14} />
          <span className="grow ellipsis" title={Object.keys(headers).join(", ")}>
            {headerCount === 1 ? t("add.headers1", { origin: headerOrigin }) : t("add.headers", { n: headerCount, origin: headerOrigin })}
            {otherOrigins ? ` ${t("add.headersOther")}` : ""}
          </span>
          <button className="icon ghost tiny" onClick={() => setHeaders({})} title={t("common.remove")} aria-label={t("common.remove")}>
            <X size={12} />
          </button>
        </div>
      )}

      <Field label={t("add.saveTo")} plain>
        <div className="folder-picker">
          <Folder size={15} />
          <span className={`grow ellipsis ${directory ? "" : "muted"}`} title={directory || defaultDirectory}>
            {directory || (magnetsOnly ? defaultDirectory : t("add.categoryFolder", { dir: defaultDirectory }))}
          </span>
          {directory && (
            <button className="small ghost" onClick={() => setDirectory("")}>
              {t("add.reset")}
            </button>
          )}
          <button className="small" onClick={pick}>
            {t("add.choose")}
          </button>
        </div>
      </Field>

      {!isBatch && !magnetsOnly && (
        <Field label={t("add.fileName")}>
          <input value={fileName} onChange={(e) => setFileName(e.target.value)} placeholder={t("add.fileNameHint")} spellCheck={false} />
        </Field>
      )}

      <button className={`disclosure ${showOptions ? "open" : ""}`} onClick={() => setShowOptions(!showOptions)}>
        <ChevronRight size={14} />
        {t("add.options")}
        {optionsSet && !showOptions && <span className="dot-accent" />}
      </button>
      {showOptions && (
        <div className="options">
          <Field label={t("add.priority")} plain>
            <Segmented
              value={priority}
              onChange={setPriority}
              options={[
                { value: -1, label: t("priority.low") },
                { value: 0, label: t("priority.normal") },
                { value: 1, label: t("priority.high") },
              ]}
            />
          </Field>
          {!magnetsOnly && <><div className="grid2">
            <Field label={t("add.connections")}>
              <input type="number" min={1} max={32} placeholder={t("add.default")} value={connections} onChange={(e) => setConnections(e.target.value)} />
            </Field>
            <Field label={t("add.speedLimit")}>
              <input placeholder={t("add.speedHint")} value={limit} onChange={(e) => setLimit(e.target.value)} />
            </Field>
          </div>
          <Field label={t("add.checksum")}>
            <input className="mono" placeholder="sha256:…" value={checksum} onChange={(e) => setChecksum(e.target.value)} spellCheck={false} />
          </Field>
          </>}
        </div>
      )}

      {error && (
        <p className="form-error" role="status">
          <CircleAlert size={14} />
          {error}
        </p>
      )}
    </Modal>
  );
}
