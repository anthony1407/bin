import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useRef,
  useState,
  type ButtonHTMLAttributes,
  type ComponentProps,
  type ReactNode,
} from "react";
import { CircleAlert, CircleCheck, X } from "lucide-react";

export function cx(...parts: (string | false | null | undefined)[]) {
  return parts.filter(Boolean).join(" ");
}

// --- buttons ----------------------------------------------------------------------

type Variant = "primary" | "secondary" | "danger" | "ghost";

const variants: Record<Variant, string> = {
  primary: "bg-accent text-on-accent hover:brightness-95 border-transparent shadow-sm",
  secondary: "bg-surface text-ink border-line-strong hover:bg-sunken",
  danger: "bg-surface text-danger border-line-strong hover:bg-danger-soft hover:border-danger",
  ghost: "bg-transparent text-muted border-transparent hover:bg-sunken hover:text-ink",
};

export function Button({
  variant = "secondary",
  busy,
  icon,
  className,
  children,
  disabled,
  ...rest
}: ButtonHTMLAttributes<HTMLButtonElement> & { variant?: Variant; busy?: boolean; icon?: ReactNode }) {
  return (
    <button
      {...rest}
      disabled={disabled || busy}
      className={cx(
        "inline-flex h-9 items-center justify-center gap-2 rounded-md border px-3.5 text-sm font-medium",
        "transition-colors disabled:cursor-not-allowed disabled:opacity-50",
        variants[variant],
        className,
      )}
    >
      {busy ? <CircularProgress className="size-4" /> : icon}
      {children}
    </button>
  );
}

export function IconButton({ label, className, children, ...rest }: ButtonHTMLAttributes<HTMLButtonElement> & { label: string }) {
  return (
    <button
      {...rest}
      aria-label={label}
      title={label}
      className={cx(
        "inline-flex size-9 items-center justify-center rounded-md text-muted transition-colors hover:bg-sunken hover:text-ink",
        className,
      )}
    >
      {children}
    </button>
  );
}

// --- form fields --------------------------------------------------------------------

export function Field({ label, hint, error, children }: { label: string; hint?: ReactNode; error?: string; children: ReactNode }) {
  return (
    <div>
      {/* Only the title and the control are in the label, so the hint is not
          read out as part of the field's name. */}
      <label className="block">
        <span className="mb-1.5 block text-sm font-medium text-ink">{label}</span>
        {children}
      </label>
      {error ? (
        <span className="mt-1.5 block text-xs text-danger">{error}</span>
      ) : hint ? (
        <span className="mt-1.5 block text-xs text-muted">{hint}</span>
      ) : null}
    </div>
  );
}

export function Input({ className, mono, ...rest }: ComponentProps<"input"> & { mono?: boolean }) {
  return (
    <input
      {...rest}
      className={cx(
        // Full width unless the caller sets one (two width classes would conflict).
        !/(^|\s)w-/.test(className ?? "") && "w-full",
        "h-9 rounded-md border border-line-strong bg-surface px-3 text-sm text-ink placeholder:text-faint",
        "focus:border-accent focus:outline-none focus:ring-2 focus:ring-accent/25",
        mono && "font-mono",
        className,
      )}
    />
  );
}

// --- surfaces ----------------------------------------------------------------------

export function Card({ className, children }: { className?: string; children: ReactNode }) {
  return <div className={cx("rounded-card border border-line bg-surface", className)}>{children}</div>;
}

export function Badge({ tone = "neutral", children }: { tone?: "neutral" | "accent" | "ok" | "warn" | "danger"; children: ReactNode }) {
  const tones = {
    neutral: "bg-sunken text-muted border-line",
    accent: "bg-accent-soft text-accent-ink border-transparent",
    ok: "bg-ok-soft text-ok border-transparent",
    warn: "bg-warn-soft text-warn border-transparent",
    danger: "bg-danger-soft text-danger border-transparent",
  };
  return (
    <span className={cx("inline-flex items-center gap-1 rounded border px-1.5 py-0.5 text-[11px] font-medium uppercase tracking-wide", tones[tone])}>
      {children}
    </span>
  );
}

/** An indeterminate circular progress: a faint track with a spinning arc. */
export function CircularProgress({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" fill="none" className={cx("shrink-0 animate-spin", className)} role="progressbar" aria-label="Loading">
      <circle cx="12" cy="12" r="9.5" stroke="currentColor" strokeWidth="2.5" opacity="0.25" />
      <path d="M21.5 12a9.5 9.5 0 0 0-9.5-9.5" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" />
    </svg>
  );
}

/** Page-level loading indicator; stays hidden when the data arrives quickly. */
export function Spinner({ className }: { className?: string }) {
  // The fade-in sits on a wrapper: the spin animation would override it on the svg.
  return (
    <span className={cx("appear-late block w-fit", className)}>
      <CircularProgress className="size-6 text-faint" />
    </span>
  );
}

export function PageHeader({ title, subtitle, actions }: { title: ReactNode; subtitle?: ReactNode; actions?: ReactNode }) {
  return (
    <div className="mb-6 flex flex-col gap-4 sm:flex-row sm:items-end sm:justify-between">
      <div className="min-w-0">
        <h1 className="truncate text-2xl font-semibold tracking-tight">{title}</h1>
        {subtitle && <p className="mt-1 text-sm text-muted">{subtitle}</p>}
      </div>
      {actions && <div className="flex flex-wrap items-center gap-2">{actions}</div>}
    </div>
  );
}

// The bin cube: a lid floating over a box whose front edge is an open slot.
// Drawn on a 100×100 grid; the round-joined stroke softens the corners.
const CUBE_LID = "M50 13 L73 25.5 L50 38 L27 25.5 Z";
const CUBE_BOX = "M20 31 L37 40 L37 60 L53 68.5 L53 46 L80 31 L80 66 L50 84 L20 66 Z";

/** The bin mark, in the brand indigo unless a text colour is given. */
export function BinMark({ className }: { className?: string }) {
  return (
    <svg viewBox="12 8 76 81" className={cx("text-accent", className)} aria-hidden="true">
      <g fill="currentColor" stroke="currentColor" strokeWidth="5" strokeLinejoin="round">
        <path d={CUBE_LID} />
        <path d={CUBE_BOX} />
      </g>
    </svg>
  );
}

/** The full logo: the mark and the "bin" letters, drawn so no font is needed. */
export function BinLogo({ className, letters = "text-ink" }: { className?: string; letters?: string }) {
  return (
    <svg viewBox="12 8 218 81" className={className} role="img" aria-label="bin">
      <g className="text-accent" fill="currentColor" stroke="currentColor" strokeWidth="5" strokeLinejoin="round">
        <path d={CUBE_LID} />
        <path d={CUBE_BOX} />
      </g>
      <g className={letters} fill="currentColor" stroke="currentColor" transform="translate(104 10) scale(0.7)">
        <rect x="0" y="10" width="17" height="90" stroke="none" />
        <circle cx="39" cy="70" r="21.5" fill="none" strokeWidth="17" />
        <rect x="82" y="42" width="17" height="58" stroke="none" />
        <circle cx="90.5" cy="20" r="10" stroke="none" />
        <path d="M123.5 100 V67 A21.5 21.5 0 0 1 166.5 67 V100" fill="none" strokeWidth="17" />
      </g>
    </svg>
  );
}

/** Empty state with an outline of the bin cube, lid lifted. */
export function EmptyState({ title, body, action }: { title: string; body?: ReactNode; action?: ReactNode }) {
  return (
    <div className="flex flex-col items-center px-6 py-16 text-center">
      <svg viewBox="12 -4 76 93" className="mb-5 h-20 text-line-strong" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinejoin="round" aria-hidden="true">
        <path d={CUBE_LID} transform="translate(0 -12)" strokeDasharray="5 5" />
        <path d={CUBE_BOX} />
      </svg>
      <h3 className="text-base font-semibold">{title}</h3>
      {body && <p className="mt-1.5 max-w-sm text-sm text-muted">{body}</p>}
      {action && <div className="mt-5">{action}</div>}
    </div>
  );
}

// --- modal ----------------------------------------------------------------------------

export function Modal({
  open,
  onClose,
  title,
  children,
  footer,
  wide,
}: {
  open: boolean;
  onClose: () => void;
  title: string;
  children: ReactNode;
  footer?: ReactNode;
  wide?: boolean;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  const openRef = useRef(open);
  openRef.current = open;
  useEffect(() => {
    const d = ref.current;
    if (!d) return;
    if (open && !d.open) d.showModal();
    if (!open && d.open) d.close();
  }, [open]);
  return (
    <dialog
      ref={ref}
      // Esc asks the owner instead of closing on its own, so a dialog with an
      // operation in progress can stay open.
      onCancel={(e) => {
        e.preventDefault();
        onClose();
      }}
      onClose={() => {
        onClose();
        // Browsers may close it anyway (a repeated Esc); reopen if the owner
        // still wants it open, so the state and the screen agree.
        setTimeout(() => {
          if (openRef.current && ref.current && !ref.current.open) ref.current.showModal();
        });
      }}
      onClick={(e) => e.target === ref.current && onClose()}
      className={cx(
        "m-auto w-[calc(100%-2rem)] rounded-card border border-line bg-surface p-0 text-ink shadow-xl backdrop:bg-black/40",
        wide ? "max-w-2xl" : "max-w-md",
      )}
    >
      {open && (
        <div className="flex max-h-[85vh] flex-col">
          <div className="flex items-center justify-between border-b border-line px-5 py-3.5">
            <h2 className="text-base font-semibold">{title}</h2>
            <IconButton label="Close" onClick={onClose} className="-mr-2">
              <X className="size-4" />
            </IconButton>
          </div>
          <div className="overflow-y-auto px-5 py-4">{children}</div>
          {footer && <div className="flex justify-end gap-2 border-t border-line bg-sunken/50 px-5 py-3">{footer}</div>}
        </div>
      )}
    </dialog>
  );
}

// --- toasts -------------------------------------------------------------------------------

type Toast = { id: number; tone: "ok" | "error"; text: string };
const ToastContext = createContext<(tone: Toast["tone"], text: string) => void>(() => {});

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([]);
  const push = useCallback((tone: Toast["tone"], text: string) => {
    const id = Date.now() + Math.random();
    setToasts((t) => [...t, { id, tone, text }]);
    setTimeout(() => setToasts((t) => t.filter((x) => x.id !== id)), tone === "error" ? 7000 : 3500);
  }, []);
  return (
    <ToastContext.Provider value={push}>
      {children}
      <div className="pointer-events-none fixed inset-x-0 bottom-4 z-50 flex flex-col items-center gap-2 px-4 sm:items-end sm:pr-6" aria-live="polite">
        {toasts.map((t) => (
          <div
            key={t.id}
            className="pointer-events-auto flex w-full max-w-sm items-start gap-2.5 rounded-card border border-line bg-surface px-4 py-3 text-sm shadow-lg"
          >
            {t.tone === "ok" ? <CircleCheck className="mt-0.5 size-4 shrink-0 text-ok" /> : <CircleAlert className="mt-0.5 size-4 shrink-0 text-danger" />}
            <span className="min-w-0 break-words">{t.text}</span>
          </div>
        ))}
      </div>
    </ToastContext.Provider>
  );
}

export function useToast() {
  return useContext(ToastContext);
}

/** Message for an unknown error value. */
export function errorText(e: unknown): string {
  return e instanceof Error ? e.message : "Something went wrong.";
}
