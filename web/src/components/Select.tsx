import { useEffect, useId, useMemo, useRef, useState, type KeyboardEvent, type ReactNode } from "react";
import { Check, ChevronsUpDown, Search } from "lucide-react";
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
  "aria-label": string;
}

// Lists that would not fit below the field open upwards.
const LIST_ROOM = 280;

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
  "aria-label": label,
}: SelectProps) {
  const id = useId();
  const listId = `${id}-list`;
  const optionId = (i: number) => `${id}-opt-${i}`;
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [active, setActive] = useState(0);
  const [above, setAbove] = useState(false);
  const root = useRef<HTMLDivElement>(null);
  const field = useRef<HTMLInputElement & HTMLButtonElement>(null);

  const selected = options.find((o) => o.value === value);
  const q = query.trim().toLowerCase();
  const shown = useMemo(() => (searchable && q ? options.filter((o) => o.label.toLowerCase().includes(q)) : options), [options, q, searchable]);

  function openList() {
    if (disabled || open) return;
    const r = root.current!.getBoundingClientRect();
    const below = window.innerHeight - r.bottom;
    setAbove(below < LIST_ROOM && r.top > below);
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

  // Click outside closes; the active option stays in view while moving.
  useEffect(() => {
    if (!open) return;
    const onDown = (e: PointerEvent) => {
      if (!root.current?.contains(e.target as Node)) close();
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
            onBlur={(e) => !root.current?.contains(e.relatedTarget) && close()}
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
        <ul
          id={listId}
          role="listbox"
          aria-label={label}
          className={cx(
            "absolute z-50 max-h-64 w-full min-w-44 overflow-y-auto rounded-md border border-line bg-surface p-1 shadow-lg",
            above ? "bottom-full mb-1" : "top-full mt-1",
          )}
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
