import { useLayoutEffect, useRef, useState, type InputHTMLAttributes } from "react";
import { Plus, Trash2 } from "lucide-react";
import { Site } from "./api";
import { useI18n, type T } from "./i18n";

const maskedPassword = "********"; // settings.Masked in the service contract.
const headerName = /^[!#$%&'*+.\-^_`|~0-9A-Za-z]+$/;
const invalidHost = /[\s/:@?#\\]/;

type HeaderDraft = { id: number; name: string; value: string };
export type SiteDraft = {
  id: number;
  original: Site;
  host: string;
  connections: string;
  hostConnections: string;
  username: string;
  password: string;
  headers: HeaderDraft[];
};

function siteId(site: SiteDraft) {
  return `site-settings-${site.id}`;
}

export function draftSite(id: number, original = new Site()): SiteDraft {
  return {
    id,
    original,
    host: original.host,
    connections: original.connections ? String(original.connections) : "",
    hostConnections: original.hostConnections ? String(original.hostConnections) : "",
    username: original.username ?? "",
    password: original.password ?? "",
    headers: Object.entries(original.headers ?? {}).map(([name, value], id) => ({ id, name, value: value ?? "" })),
  };
}

export function siteSettingsErrors(sites: SiteDraft[], t: T) {
  const errors = new Map<string, string>();
  for (const site of sites) {
    const id = siteId(site);
    const host = site.host.trim();
    // The editor is stricter about new entries. Keep previously accepted values intact.
    if ((!site.original.host || site.host !== site.original.host) && (!host || invalidHost.test(host))) {
      errors.set(`${id}-host`, t("settings.siteHostError"));
    }
    for (const [field, max] of [["connections", 32], ["hostConnections", 64]] as const) {
      const input = site[field].trim();
      if (input && (!/^\d+$/.test(input) || Number(input) > max)) {
        errors.set(`${id}-${field}`, t("settings.siteConnectionsError", { max }));
      }
    }
    if (site.password === maskedPassword && (site.original.password !== maskedPassword || site.host.toLowerCase() !== site.original.host.toLowerCase())) {
      errors.set(`${id}-password`, t("settings.sitePasswordError"));
    }
    const seenNames = new Set<string>();
    for (const header of site.headers) {
      const field = `${id}-header-${header.id}-name`;
      const originalName = Object.prototype.hasOwnProperty.call(site.original.headers ?? {}, header.name);
      if (!header.name || (!originalName && !headerName.test(header.name))) {
        errors.set(field, t("settings.siteHeaderNameError"));
      } else if (seenNames.has(header.name)) {
        errors.set(field, t("settings.siteHeaderDuplicate"));
      }
      seenNames.add(header.name);
    }
  }
  return errors;
}

export function siteValues(sites: SiteDraft[]): Site[] {
  return sites.map((site) => new Site({
    host: site.host,
    connections: Number(site.connections) || undefined,
    hostConnections: Number(site.hostConnections) || undefined,
    username: site.username || undefined,
    password: site.password || undefined,
    headers: site.headers.length ? Object.fromEntries(site.headers.map(({ name, value }) => [name, value])) : undefined,
  }));
}

function SiteField({ id, label, hint, error, ...input }: InputHTMLAttributes<HTMLInputElement> & {
  id: string;
  label: string;
  hint?: string;
  error?: string;
}) {
  const description = [hint && `${id}-hint`, error && `${id}-error`].filter(Boolean).join(" ");
  return (
    <div className="field">
      <label className="field-label" htmlFor={id}>{label}</label>
      <input {...input} id={id} aria-invalid={!!error} aria-describedby={description || undefined} />
      {hint && <span className="hint" id={`${id}-hint`}>{hint}</span>}
      {error && <span className="form-error" id={`${id}-error`}>{error}</span>}
    </div>
  );
}

export default function SiteSettings({ sites, onChange, errors, focusInvalid }: {
  sites: SiteDraft[];
  onChange: (sites: SiteDraft[]) => void;
  errors: Map<string, string>;
  focusInvalid: number;
}) {
  const { t } = useI18n();
  const section = useRef<HTMLElement>(null);
  const addSite = useRef<HTMLButtonElement>(null);
  const [focusID, setFocusID] = useState("");

  useLayoutEffect(() => {
    if (focusInvalid) section.current?.querySelector<HTMLInputElement>('input[aria-invalid="true"]')?.focus();
  }, [focusInvalid]);

  useLayoutEffect(() => {
    if (focusID) {
      document.getElementById(focusID)?.focus();
      setFocusID("");
    }
  }, [focusID]);

  function updateSite(id: number, patch: Partial<SiteDraft>) {
    onChange(sites.map((site) => site.id === id ? { ...site, ...patch } : site));
  }

  return (
    <section ref={section} aria-labelledby="site-settings-title" className="site-settings">
      <div className="section-head">
        <div>
          <h3 className="set-label" id="site-settings-title">{t("settings.sites")}</h3>
          <p className="hint">{t("settings.sitesHint")}</p>
        </div>
        <button ref={addSite} type="button" className="small" onClick={() => {
          const site = draftSite((sites[sites.length - 1]?.id ?? -1) + 1);
          onChange([...sites, site]);
          setFocusID(`${siteId(site)}-host`);
        }}>
          <Plus size={14} aria-hidden="true" />
          {t("settings.addSite")}
        </button>
      </div>
      {sites.length === 0 && <p className="muted empty-rules">{t("settings.sitesEmpty")}</p>}
      {sites.map((site) => {
        const id = siteId(site);
        const title = site.host.trim() || t("settings.newSite");
        const field = (key: "host" | "connections" | "hostConnections" | "username" | "password") => ({
          id: `${id}-${key}`,
          value: site[key],
          error: errors.get(`${id}-${key}`),
          onChange: (event: React.ChangeEvent<HTMLInputElement>) => updateSite(site.id, { [key]: event.target.value }),
        });
        return (
          <fieldset className="site-rule" key={site.id}>
            <legend>{title}</legend>
            <SiteField {...field("host")} label={t("settings.siteHost")} placeholder="example.com" required spellCheck={false} />
            <div className="site-field-pair">
              <SiteField {...field("connections")} label={t("settings.connections")} inputMode="numeric" placeholder={t("add.default")} />
              <SiteField {...field("hostConnections")} label={t("settings.hostConnections")} inputMode="numeric" placeholder={t("add.default")} />
            </div>
            <div className="site-field-pair">
              <SiteField {...field("username")} label={t("settings.siteUsername")} autoComplete="off" spellCheck={false} />
              <SiteField {...field("password")} label={t("settings.sitePassword")} type="password" autoComplete="new-password"
                hint={site.original.password === maskedPassword ? t("settings.sitePasswordSaved") : undefined} />
            </div>

            <fieldset className="site-headers">
              <legend className="field-label">{t("settings.siteHeaders")}</legend>
              {site.headers.map((header) => {
                const headerID = `${id}-header-${header.id}`;
                const update = (patch: Partial<HeaderDraft>) => updateSite(site.id, {
                  headers: site.headers.map((entry) => entry.id === header.id ? { ...entry, ...patch } : entry),
                });
                return (
                  <div className="site-header" key={header.id}>
                    <SiteField id={`${headerID}-name`} label={t("settings.siteHeaderName")} required spellCheck={false}
                      error={errors.get(`${headerID}-name`)} value={header.name} onChange={(event) => update({ name: event.target.value })} />
                    <SiteField id={`${headerID}-value`} label={t("settings.siteHeaderValue")} spellCheck={false}
                      value={header.value} onChange={(event) => update({ value: event.target.value })} />
                    <button type="button" className="icon ghost" aria-label={t("settings.removeHeader")} onClick={() => {
                      updateSite(site.id, { headers: site.headers.filter((entry) => entry.id !== header.id) });
                      setFocusID(`${id}-add-header`);
                    }}>
                      <Trash2 size={15} aria-hidden="true" />
                    </button>
                  </div>
                );
              })}
              <button id={`${id}-add-header`} type="button" className="small" onClick={() => {
                const header = { id: (site.headers[site.headers.length - 1]?.id ?? -1) + 1, name: "", value: "" };
                updateSite(site.id, { headers: [...site.headers, header] });
                setFocusID(`${id}-header-${header.id}-name`);
              }}>
                <Plus size={14} aria-hidden="true" />
                {t("settings.addHeader")}
              </button>
            </fieldset>
            <button type="button" className="small" onClick={() => {
              onChange(sites.filter((entry) => entry.id !== site.id));
              addSite.current?.focus();
            }}>
              <Trash2 size={15} aria-hidden="true" />
              {t("settings.removeSite")}
            </button>
          </fieldset>
        );
      })}
    </section>
  );
}
