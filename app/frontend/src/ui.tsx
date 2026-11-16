import { useEffect, useId, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { X, type LucideIcon } from "lucide-react";
import { useI18n } from "./i18n";

/** Modal is a dialog that closes on Escape and on a backdrop click. */
export function Modal({
  icon: Icon,
  tone = "accent",
  title,
  size = "",
  onClose,
  focusFallback,
  children,
  footer,
}: {
  icon?: LucideIcon;
  tone?: "accent" | "danger" | "warn";
  title: ReactNode;
  size?: "" | "small" | "wide";
  onClose: () => void;
  focusFallback?: () => void;
  children: ReactNode;
  footer?: ReactNode;
}) {
  const { t } = useI18n();
  const titleId = useId();
  const ref = useRef<HTMLDialogElement>(null);
  useLayoutEffect(() => {
    const dialog = ref.current!;
    const trigger = document.activeElement;
    const initial = dialog.querySelector("[data-initial-focus]");
    // React's autoFocus prop focuses on mount, before a native dialog is open.
    initial?.setAttribute("autofocus", "");
    dialog.setAttribute("closedby", "any");
    dialog.showModal();
    return () => {
      dialog.close();
      const active = document.activeElement;
      if (trigger && !trigger.isConnected && (active === document.body || dialog.contains(active))) {
        focusFallback?.();
      }
    };
  }, [focusFallback]);
  return (
    <dialog
      ref={ref}
      className={`modal ${size}`}
      aria-labelledby={titleId}
      onCancel={(event) => {
        event.preventDefault();
        onClose();
      }}
      onMouseDown={(event) => {
        if ("closedBy" in HTMLDialogElement.prototype || event.target !== event.currentTarget) return;
        const { left, right, top, bottom } = event.currentTarget.getBoundingClientRect();
        if (event.clientX < left || event.clientX > right || event.clientY < top || event.clientY > bottom) {
          event.preventDefault();
          onClose();
        }
      }}
    >
      <header className="modal-head">
        {Icon && (
          <span className={`modal-icon ${tone}`}>
            <Icon size={18} strokeWidth={2} />
          </span>
        )}
        <h2 id={titleId}>{title}</h2>
        <button type="button" className="icon ghost" onClick={onClose} aria-label={t("common.close")} title={t("common.close")}>
          <X size={16} />
        </button>
      </header>
      <div className="modal-body">{children}</div>
      {footer && <footer className="modal-foot">{footer}</footer>}
    </dialog>
  );
}

export type MenuEntry = { icon?: LucideIcon; label: string; onSelect: () => void; checked?: boolean; danger?: boolean } | "separator";

/** Menu is a button that opens a list of actions below it. */
export function Menu({ trigger, entries, align = "right" }: { trigger: (open: boolean) => ReactNode; entries: MenuEntry[]; align?: "left" | "right" }) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    const close = (e: MouseEvent) => !ref.current?.contains(e.target as Node) && setOpen(false);
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && setOpen(false);
    window.addEventListener("mousedown", close);
    window.addEventListener("keydown", onKey);
    return () => {
      window.removeEventListener("mousedown", close);
      window.removeEventListener("keydown", onKey);
    };
  }, [open]);
  return (
    <div className="menu-anchor" ref={ref} onClick={() => setOpen(!open)}>
      {trigger(open)}
      {open && (
        <div className={`menu ${align}`} role="menu" onClick={(e) => e.stopPropagation()}>
          {entries.map((e, i) =>
            e === "separator" ? (
              <div key={i} className="menu-sep" />
            ) : (
              <button
                key={i}
                role="menuitem"
                className={`menu-item ${e.danger ? "destructive" : ""} ${e.checked ? "checked" : ""}`}
                onClick={() => {
                  setOpen(false);
                  e.onSelect();
                }}
              >
                {e.icon ? <e.icon size={15} /> : <span className="menu-gap" />}
                <span className="grow">{e.label}</span>
                {e.checked && <span className="menu-dot" />}
              </button>
            ),
          )}
        </div>
      )}
    </div>
  );
}

export function Switch({ checked, onChange, label, hint }: { checked: boolean; onChange: (v: boolean) => void; label: ReactNode; hint?: ReactNode }) {
  return (
    <label className="switch-row">
      <span className="grow">
        <span className="switch-label">{label}</span>
        {hint && <span className="hint">{hint}</span>}
      </span>
      <input type="checkbox" className="switch" checked={checked} onChange={(e) => onChange(e.target.checked)} />
    </label>
  );
}

export function Segmented<V extends string | number>({ value, options, onChange }: { value: V; options: { value: V; label: string; icon?: LucideIcon }[]; onChange: (v: V) => void }) {
  return (
    <div className="segmented" role="radiogroup">
      {options.map((o) => (
        <button key={String(o.value)} role="radio" aria-checked={o.value === value} className={o.value === value ? "on" : ""} onClick={() => onChange(o.value)}>
          {o.icon && <o.icon size={14} />}
          {o.label}
        </button>
      ))}
    </div>
  );
}

/** Field is a labelled form control with an optional hint. A plain field
 * holds buttons, which a <label> would activate on any click. */
export function Field({ label, hint, children, plain }: { label: ReactNode; hint?: ReactNode; children: ReactNode; plain?: boolean }) {
  const Tag = plain ? "div" : "label";
  return (
    <Tag className="field">
      <span className="field-label">{label}</span>
      {children}
      {hint && <span className="hint">{hint}</span>}
    </Tag>
  );
}
