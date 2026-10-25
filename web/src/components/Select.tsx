import {
  useEffect,
  useId,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type CSSProperties,
  type KeyboardEvent,
  type ReactNode,
  type RefObject,
} from "react";
import { createPortal } from "react-dom";
import { Check, ChevronsUpDown, Search, X } from "lucide-react";
import { cx } from "./ui";

export interface Option {
  value: string;
  label: string;
  /** Secondary text on the right of the option, e.g. a size. */
  hint?: ReactNode;
  /** Overrides the select's `mono` for this option. */
  mono?: boolean;
}

interface SelectProps {
  value: string;
  onChange: (value: string) => void;
  options: Option[];
  /** Autocomplete: the field becomes a text box that filters the options. */
  searchable?: boolean;
  placeholder?: string;
  emptyText?: string;
  disabled?: boolean;
  mono?: boolean;
  className?: string;
  /** Id of the field, for a visible <label htmlFor>. */
  id?: string;
  "aria-label": string;
}

// Lists that would not fit below the field open upwards.
const LIST_ROOM = 280;
const LIST_MAX_HEIGHT = 256;
const LIST_GAP = 4;

/**
 * Places an open list next to its field with fixed positioning, rendered in a
 * portal (see FloatingList), so no scrolling or clipping container around the
 * field (a modal's body, a card) cuts it off or grows a scroll bar. It follows
 * the field while anything scrolls or the window resizes.
 */
function useFloatingPosition(anchor: RefObject<HTMLElement | null>, open: boolean): CSSProperties {
  const [style, setStyle] = useState<CSSProperties>({ position: "fixed", visibility: "hidden" });
  useLayoutEffect(() => {
    if (!open) return;
    const update = () => {
      const el = anchor.current;
      if (!el) return;
      const r = el.getBoundingClientRect();
      const below = window.innerHeight - r.bottom - LIST_GAP * 2;
      const above = r.top - LIST_GAP * 2;
      const up = below < LIST_ROOM && above > below;
      const maxHeight = Math.max(120, Math.min(LIST_MAX_HEIGHT, up ? above : below));
      setStyle({
        position: "fixed",
        left: r.left,
        width: r.width,
        maxHeight,
        ...(up ? { bottom: window.innerHeight - r.top + LIST_GAP } : { top: r.bottom + LIST_GAP }),
      });
    };
    update();
    window.addEventListener("scroll", update, true); // any scrolling ancestor
    window.addEventListener("resize", update);
    return () => {
      window.removeEventListener("scroll", update, true);
      window.removeEventListener("resize", update);
    };
  }, [anchor, open]);
  return style;
}

/**
 * Renders a list outside the field's containers. Inside a modal <dialog> it
 * must stay in the dialog: everything outside an open modal dialog is inert.
 */
function FloatingList({ anchor, children }: { anchor: RefObject<HTMLElement | null>; children: ReactNode }) {
  const host = anchor.current?.closest("dialog") ?? document.body;
  return createPortal(children, host);
}

/**
 * A styled replacement for <select>, following the ARIA combobox pattern:
 * arrow keys, Home/End, Enter and Esc work as in a native select, and with
 * `searchable` typing filters the options.
 */
export function Select({
  value,
  onChange,
  options,
  searchable,
  placeholder = "Select…",
  emptyText = "No matches",
  disabled,
  mono,
  className,
  id: fieldId,
  "aria-label": label,
}: SelectProps) {
  const id = useId();
  const listId = `${id}-list`;
  const optionId = (i: number) => `${id}-opt-${i}`;
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [active, setActive] = useState(0);
  const root = useRef<HTMLDivElement>(null);
  const list = useRef<HTMLUListElement>(null);
  const field = useRef<HTMLInputElement & HTMLButtonElement>(null);
  const listStyle = useFloatingPosition(root, open);

  const selected = options.find((o) => o.value === value);
  const q = query.trim().toLowerCase();
  const shown = useMemo(() => (searchable && q ? options.filter((o) => o.label.toLowerCase().includes(q)) : options), [options, q, searchable]);

  function openList() {
    if (disabled || open) return;
    setActive(Math.max(0, options.findIndex((o) => o.value === value)));
    setOpen(true);
  }
  function close() {
    setOpen(false);
    setQuery("");
  }
  function choose(o: Option) {
    onChange(o.value);
    close();
    field.current?.focus();
  }

  // Click outside closes (the list is portaled, so it counts as inside); the
  // active option stays in view while moving.
  useEffect(() => {
    if (!open) return;
    const onDown = (e: PointerEvent) => {
      const t = e.target as Node;
      if (!root.current?.contains(t) && !list.current?.contains(t)) close();
    };
    document.addEventListener("pointerdown", onDown);
    return () => document.removeEventListener("pointerdown", onDown);
  }, [open]);
  useEffect(() => {
    if (open) document.getElementById(optionId(active))?.scrollIntoView({ block: "nearest" });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [active, open]);

  function onKeyDown(e: KeyboardEvent) {
    const last = shown.length - 1;
    switch (e.key) {
      case "ArrowDown":
      case "ArrowUp":
        e.preventDefault();
        if (!open) openList();
        else setActive((i) => (e.key === "ArrowDown" ? Math.min(last, i + 1) : Math.max(0, i - 1)));
        break;
      case "Home":
      case "End":
        if (open && !searchable) {
          e.preventDefault();
          setActive(e.key === "Home" ? 0 : last);
        }
        break;
      case "Enter":
        e.preventDefault();
        if (open) shown[active] && choose(shown[active]);
        else openList();
        break;
      case " ":
        if (searchable) break; // a space is part of the search text
        e.preventDefault();
        if (open) shown[active] && choose(shown[active]);
        else openList();
        break;
      case "Escape":
        if (open) {
          e.preventDefault();
          e.stopPropagation();
          close();
        }
        break;
      case "Tab":
        close();
        break;
    }
  }

  const fieldClass = cx(
    "h-9 w-full rounded-md border border-line-strong bg-surface text-sm text-ink",
    "focus:border-accent focus:ring-2 focus:ring-accent/25 focus:outline-none",
    "disabled:cursor-not-allowed disabled:opacity-50",
    mono && "font-mono",
  );
  const comboboxProps = {
    id: fieldId ?? `${id}-field`,
    role: "combobox" as const,
    "aria-label": label,
    "aria-expanded": open,
    "aria-controls": listId,
    "aria-activedescendant": open && shown[active] ? optionId(active) : undefined,
    disabled,
    onKeyDown,
  };

  return (
    <div ref={root} className={cx("relative", className)}>
      {searchable ? (
        <>
          <Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-faint" />
          <input
            {...comboboxProps}
            ref={field}
            aria-autocomplete="list"
            autoComplete="off"
            spellCheck={false}
            value={open ? query : (selected?.label ?? "")}
            placeholder={open ? (selected?.label ?? placeholder) : placeholder}
            onChange={(e) => {
              setQuery(e.target.value);
              setActive(0);
              openList();
            }}
            onClick={openList}
            onBlur={(e) => !root.current?.contains(e.relatedTarget) && !list.current?.contains(e.relatedTarget) && close()}
            className={cx(fieldClass, "pr-9 pl-9 placeholder:text-faint")}
          />
        </>
      ) : (
        <button
          {...comboboxProps}
          ref={field}
          type="button"
          aria-haspopup="listbox"
          onClick={() => (open ? close() : openList())}
          className={cx(fieldClass, "flex items-center pr-9 pl-3 text-left")}
        >
          <span className={cx("truncate", !selected && "text-faint")}>{selected?.label ?? placeholder}</span>
        </button>
      )}
      <ChevronsUpDown className="pointer-events-none absolute top-1/2 right-3 size-4 -translate-y-1/2 text-faint" />

      {open && (
        <FloatingList anchor={root}>
        <ul
          ref={list}
          id={listId}
          role="listbox"
          aria-label={label}
          style={listStyle}
          // Clicks on the list (its scroll bar included) keep focus in the field.
          onMouseDown={(e) => e.preventDefault()}
          className="z-50 min-w-44 overflow-y-auto rounded-md border border-line bg-surface p-1 text-ink shadow-lg"
        >
          {shown.length === 0 && <li className="px-3 py-2 text-sm text-faint">{emptyText}</li>}
          {shown.map((o, i) => {
            const isSelected = o.value === value;
            return (
              <li
                key={o.value}
                id={optionId(i)}
                role="option"
                aria-selected={isSelected}
                // Keep focus in the field, so the list does not close on blur.
                onPointerDown={(e) => e.preventDefault()}
                onPointerMove={() => setActive(i)}
                onClick={() => choose(o)}
                className={cx(
                  "flex cursor-pointer items-center gap-2 rounded px-2 py-1.5 text-sm",
                  i === active && "bg-sunken",
                  isSelected && "font-medium text-accent-ink",
                )}
              >
                <Check className={cx("size-4 shrink-0", isSelected ? "text-accent" : "invisible")} />
                <span className={cx("min-w-0 truncate", (o.mono ?? mono) ? "font-mono" : "font-sans")}>
                  <Highlight text={o.label} query={searchable ? q : ""} />
                </span>
                {o.hint && <span className="ml-auto shrink-0 pl-3 text-xs font-normal text-muted">{o.hint}</span>}
              </li>
            );
          })}
        </ul>
        </FloatingList>
      )}
    </div>
  );
}

interface MultiSelectProps {
  values: string[];
  onChange: (values: string[]) => void;
  options: Option[];
  /** Label of the first option, chosen when nothing else is (e.g. "All buckets"). */
  noneLabel: string;
  noneHint?: ReactNode;
  emptyText?: string;
  disabled?: boolean;
  mono?: boolean;
  className?: string;
  "aria-label": string;
}

/**
 * An autocomplete that picks several options, shown as removable chips.
 * The list stays open while choosing; Backspace in the empty search box
 * removes the last chip, and the first option clears the selection.
 */
export function MultiSelect({
  values,
  onChange,
  options,
  noneLabel,
  noneHint,
  emptyText = "No matches",
  disabled,
  mono,
  className,
  "aria-label": label,
}: MultiSelectProps) {
  const id = useId();
  const listId = `${id}-list`;
  const optionId = (i: number) => `${id}-opt-${i}`;
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [active, setActive] = useState(0);
  const root = useRef<HTMLDivElement>(null);
  const list = useRef<HTMLUListElement>(null);
  const input = useRef<HTMLInputElement>(null);
  const listStyle = useFloatingPosition(root, open);

  const q = query.trim().toLowerCase();
  const none: Option = { value: "", label: noneLabel, hint: noneHint, mono: false };
  // The "none" entry only makes sense while not searching.
  const shown = useMemo(() => (q ? options.filter((o) => o.label.toLowerCase().includes(q)) : [none, ...options]), [options, q, noneLabel, noneHint]); // eslint-disable-line react-hooks/exhaustive-deps
  const labelOf = (v: string) => options.find((o) => o.value === v)?.label ?? v;

  function openList() {
    if (disabled || open) return;
    setOpen(true);
  }
  function close() {
    setOpen(false);
    setQuery("");
    setActive(0);
  }
  function toggle(o: Option) {
    if (o.value === "") onChange([]);
    else onChange(values.includes(o.value) ? values.filter((v) => v !== o.value) : [...values, o.value]);
    setQuery("");
    input.current?.focus();
  }

  useEffect(() => {
    if (!open) return;
    const onDown = (e: PointerEvent) => {
      const t = e.target as Node;
      if (!root.current?.contains(t) && !list.current?.contains(t)) close();
    };
    document.addEventListener("pointerdown", onDown);
    return () => document.removeEventListener("pointerdown", onDown);
  }, [open]);
  useEffect(() => {
    if (open) document.getElementById(optionId(active))?.scrollIntoView({ block: "nearest" });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [active, open]);

  function onKeyDown(e: KeyboardEvent) {
    const last = shown.length - 1;
    switch (e.key) {
      case "ArrowDown":
      case "ArrowUp":
        e.preventDefault();
        if (!open) openList();
        else setActive((i) => (e.key === "ArrowDown" ? Math.min(last, i + 1) : Math.max(0, i - 1)));
        break;
      case "Enter":
        e.preventDefault();
        if (open) shown[active] && toggle(shown[active]);
        else openList();
        break;
      case "Backspace":
        if (!query && values.length) onChange(values.slice(0, -1));
        break;
      case "Escape":
        if (open) {
          e.preventDefault();
          e.stopPropagation();
          close();
        }
        break;
      case "Tab":
        close();
        break;
    }
  }

  return (
    <div ref={root} className={cx("relative", className)}>
      <div
        onClick={() => {
          input.current?.focus();
          openList();
        }}
        className={cx(
          "flex min-h-9 w-full cursor-text flex-wrap items-center gap-1 rounded-md border border-line-strong bg-surface py-1 pr-9 pl-9 text-sm",
          "focus-within:border-accent focus-within:ring-2 focus-within:ring-accent/25",
          disabled && "cursor-not-allowed opacity-50",
        )}
      >
        <Search className="pointer-events-none absolute top-2.5 left-3 size-4 text-faint" />
        {values.map((v) => (
          <span key={v} className={cx("inline-flex max-w-full items-center gap-0.5 rounded bg-accent-soft py-0.5 pr-0.5 pl-1.5 text-xs text-accent-ink", mono && "font-mono")}>
            <span className="truncate">{labelOf(v)}</span>
            <button
              type="button"
              disabled={disabled}
              aria-label={`Remove ${labelOf(v)}`}
              onClick={(e) => {
                e.stopPropagation();
                onChange(values.filter((x) => x !== v));
              }}
              className="rounded p-0.5 hover:bg-accent/20"
            >
              <X className="size-3" />
            </button>
          </span>
        ))}
        <input
          ref={input}
          id={`${id}-field`}
          role="combobox"
          aria-label={label}
          aria-expanded={open}
          aria-controls={listId}
          aria-autocomplete="list"
          aria-activedescendant={open && shown[active] ? optionId(active) : undefined}
          autoComplete="off"
          spellCheck={false}
          disabled={disabled}
          value={query}
          placeholder={values.length ? "" : noneLabel}
          onChange={(e) => {
            setQuery(e.target.value);
            setActive(0);
            openList();
          }}
          onKeyDown={onKeyDown}
          onBlur={(e) => !root.current?.contains(e.relatedTarget) && !list.current?.contains(e.relatedTarget) && close()}
          className={cx("h-7 min-w-16 flex-1 bg-transparent text-ink placeholder:text-faint focus:outline-none disabled:cursor-not-allowed", mono && "font-mono")}
        />
      </div>
      <ChevronsUpDown className="pointer-events-none absolute top-2.5 right-3 size-4 text-faint" />

      {open && (
        <FloatingList anchor={root}>
        <ul
          ref={list}
          id={listId}
          role="listbox"
          aria-label={label}
          aria-multiselectable="true"
          style={listStyle}
          // Clicks on the list (its scroll bar included) keep focus in the field.
          onMouseDown={(e) => e.preventDefault()}
          className="z-50 min-w-44 overflow-y-auto rounded-md border border-line bg-surface p-1 text-ink shadow-lg"
        >
          {shown.length === 0 && <li className="px-3 py-2 text-sm text-faint">{emptyText}</li>}
          {shown.map((o, i) => {
            const checked = o.value === "" ? values.length === 0 : values.includes(o.value);
            return (
              <li
                key={o.value || "(none)"}
                id={optionId(i)}
                role="option"
                aria-selected={checked}
                onPointerDown={(e) => e.preventDefault()}
                onPointerMove={() => setActive(i)}
                onClick={() => toggle(o)}
                className={cx(
                  "flex cursor-pointer items-center gap-2 rounded px-2 py-1.5 text-sm",
                  i === active && "bg-sunken",
                  o.value === "" && "border-b border-line",
                )}
              >
                <span
                  className={cx(
                    "flex size-4 shrink-0 items-center justify-center rounded border",
                    checked ? "border-accent bg-accent text-on-accent" : "border-line-strong",
                    o.value === "" && "rounded-full",
                  )}
                >
                  {checked && <Check className="size-3" strokeWidth={3} />}
                </span>
                <span className={cx("min-w-0 truncate", (o.mono ?? mono) ? "font-mono" : "font-sans", checked && "font-medium")}>
                  <Highlight text={o.label} query={q} />
                </span>
                {o.hint && <span className="ml-auto shrink-0 pl-3 text-xs text-muted">{o.hint}</span>}
              </li>
            );
          })}
        </ul>
        </FloatingList>
      )}
    </div>
  );
}

/** The label with the part matching the search in bold. */
function Highlight({ text, query }: { text: string; query: string }) {
  const at = query ? text.toLowerCase().indexOf(query) : -1;
  if (at < 0) return <>{text}</>;
  return (
    <>
      {text.slice(0, at)}
      <mark className="bg-transparent font-semibold text-ink underline decoration-accent decoration-2 underline-offset-2">{text.slice(at, at + query.length)}</mark>
      {text.slice(at + query.length)}
    </>
  );
}
