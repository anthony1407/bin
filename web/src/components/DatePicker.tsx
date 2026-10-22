import { useEffect, useId, useMemo, useRef, useState } from "react";
import { Calendar, ChevronLeft, ChevronRight, Infinity as InfinityIcon, X } from "lucide-react";
import { cx } from "./ui";

interface DatePickerProps {
  value: string; // YYYY-MM-DD or ""
  onChange: (value: string) => void;
  min?: string; // YYYY-MM-DD
  placeholder?: string;
  disabled?: boolean;
  className?: string;
}

const MONTH_NAMES = [
  "January", "February", "March", "April", "May", "June",
  "July", "August", "September", "October", "November", "December"
];

const WEEK_DAYS = ["Su", "Mo", "Tu", "We", "Th", "Fr", "Sa"];

function formatDisplayDate(isoDate: string): string {
  if (!isoDate) return "";
  const [year, month, day] = isoDate.split("-").map(Number);
  if (!year || !month || !day) return isoDate;
  const d = new Date(year, month - 1, day);
  return d.toLocaleDateString(undefined, {
    month: "short",
    day: "numeric",
    year: "numeric",
  });
}

function addDaysISO(days: number): string {
  const d = new Date();
  d.setDate(d.getDate() + days);
  return d.toISOString().slice(0, 10);
}

export function DatePicker({
  value,
  onChange,
  min,
  placeholder = "Select date…",
  disabled = false,
  className,
}: DatePickerProps) {
  const id = useId();
  const [open, setOpen] = useState(false);
  const containerRef = useRef<HTMLDivElement>(null);

  const todayStr = useMemo(() => new Date().toISOString().slice(0, 10), []);
  const effectiveMin = min ?? todayStr;

  // View state for month/year navigation
  const initialDate = value ? new Date(`${value}T12:00:00`) : new Date();
  const [viewYear, setViewYear] = useState(initialDate.getFullYear());
  const [viewMonth, setViewMonth] = useState(initialDate.getMonth());

  // Update view if value changes externally and is valid
  useEffect(() => {
    if (value) {
      const parts = value.split("-").map(Number);
      if (parts.length === 3 && !isNaN(parts[0]) && !isNaN(parts[1])) {
        setViewYear(parts[0]);
        setViewMonth(parts[1] - 1);
      }
    }
  }, [value]);

  // Close when clicking outside
  useEffect(() => {
    if (!open) return;
    function handleClickOutside(e: MouseEvent) {
      if (containerRef.current && !containerRef.current.contains(e.target as Node)) {
        setOpen(false);
      }
    }
    function handleKeyDown(e: KeyboardEvent) {
      if (e.key === "Escape") {
        setOpen(false);
      }
    }
    document.addEventListener("mousedown", handleClickOutside);
    document.addEventListener("keydown", handleKeyDown);
    return () => {
      document.removeEventListener("mousedown", handleClickOutside);
      document.removeEventListener("keydown", handleKeyDown);
    };
  }, [open]);

  const daysInMonth = new Date(viewYear, viewMonth + 1, 0).getDate();
  const firstDayOfWeek = new Date(viewYear, viewMonth, 1).getDay();

  function prevMonth() {
    if (viewMonth === 0) {
      setViewMonth(11);
      setViewYear((y) => y - 1);
    } else {
      setViewMonth((m) => m - 1);
    }
  }

  function nextMonth() {
    if (viewMonth === 11) {
      setViewMonth(0);
      setViewYear((y) => y + 1);
    } else {
      setViewMonth((m) => m + 1);
    }
  }

  function handleSelect(day: number) {
    const formattedMonth = String(viewMonth + 1).padStart(2, "0");
    const formattedDay = String(day).padStart(2, "0");
    const dateStr = `${viewYear}-${formattedMonth}-${formattedDay}`;

    if (effectiveMin && dateStr < effectiveMin) return;

    onChange(dateStr);
    setOpen(false);
  }

  function handleQuickSet(days: number) {
    onChange(addDaysISO(days));
    setOpen(false);
  }

  function handleNeverExpire() {
    onChange("");
    setOpen(false);
  }

  const calendarGrid = useMemo(() => {
    const cells = [];
    // Padding cells before the 1st day of the month
    for (let i = 0; i < firstDayOfWeek; i++) {
      cells.push({ type: "empty" as const, key: `empty-${i}` });
    }
    // Month days
    for (let d = 1; d <= daysInMonth; d++) {
      const formattedMonth = String(viewMonth + 1).padStart(2, "0");
      const formattedDay = String(d).padStart(2, "0");
      const dateStr = `${viewYear}-${formattedMonth}-${formattedDay}`;
      const isPastMin = effectiveMin ? dateStr < effectiveMin : false;
      const isSelected = value === dateStr;
      const isToday = todayStr === dateStr;

      cells.push({
        type: "day" as const,
        key: dateStr,
        day: d,
        dateStr,
        disabled: isPastMin,
        isSelected,
        isToday,
      });
    }
    return cells;
  }, [firstDayOfWeek, daysInMonth, viewYear, viewMonth, effectiveMin, value, todayStr]);

  return (
    <div ref={containerRef} className={cx("relative inline-block w-full", className)}>
      {/* Trigger Button */}
      <div className="relative flex items-center">
        <button
          type="button"
          id={id}
          disabled={disabled}
          onClick={() => setOpen((prev) => !prev)}
          className={cx(
            "flex h-9 w-full items-center justify-between rounded-md border border-line-strong bg-surface px-3 text-sm text-ink",
            "transition-all duration-150 focus:border-accent focus:outline-none focus:ring-2 focus:ring-accent/25",
            disabled && "cursor-not-allowed opacity-50 bg-sunken",
            open && "border-accent ring-2 ring-accent/25"
          )}
        >
          <span className="flex items-center gap-2 truncate">
            <Calendar className="size-4 shrink-0 text-faint" />
            {value ? (
              <span className="font-medium text-ink">{formatDisplayDate(value)}</span>
            ) : (
              <span className="text-faint">{placeholder}</span>
            )}
          </span>
          <span className="shrink-0 text-xs text-faint">
            {value ? value : ""}
          </span>
        </button>

        {/* Clear / Never button inside trigger */}
        {value && !disabled && (
          <button
            type="button"
            title="Set to never expire"
            aria-label="Clear date"
            onClick={(e) => {
              e.stopPropagation();
              onChange("");
            }}
            className="absolute right-2 flex size-5 items-center justify-center rounded text-faint hover:bg-sunken hover:text-ink transition-colors"
          >
            <X className="size-3.5" />
          </button>
        )}
      </div>

      {/* Popover Calendar */}
      {open && !disabled && (
        <div
          role="dialog"
          aria-label="Calendar date picker"
          className="absolute left-0 z-50 mt-1.5 w-72 rounded-xl border border-line-strong bg-surface p-3.5 shadow-2xl backdrop-blur animate-in fade-in zoom-in-95 duration-100"
        >
          {/* Header navigation */}
          <div className="mb-3 flex items-center justify-between">
            <button
              type="button"
              onClick={prevMonth}
              className="flex size-7 items-center justify-center rounded-md text-muted transition-colors hover:bg-sunken hover:text-ink"
              aria-label="Previous month"
            >
              <ChevronLeft className="size-4" />
            </button>
            <div className="text-sm font-semibold text-ink">
              {MONTH_NAMES[viewMonth]} {viewYear}
            </div>
            <button
              type="button"
              onClick={nextMonth}
              className="flex size-7 items-center justify-center rounded-md text-muted transition-colors hover:bg-sunken hover:text-ink"
              aria-label="Next month"
            >
              <ChevronRight className="size-4" />
            </button>
          </div>

          {/* Weekday headers */}
          <div className="mb-1 grid grid-cols-7 text-center">
            {WEEK_DAYS.map((d) => (
              <div key={d} className="py-0.5 text-[11px] font-medium uppercase tracking-wider text-faint">
                {d}
              </div>
            ))}
          </div>

          {/* Days grid */}
          <div className="grid grid-cols-7 gap-1">
            {calendarGrid.map((cell) => {
              if (cell.type === "empty") {
                return <div key={cell.key} className="size-8" />;
              }

              return (
                <button
                  key={cell.key}
                  type="button"
                  disabled={cell.disabled}
                  onClick={() => handleSelect(cell.day)}
                  className={cx(
                    "flex size-8 items-center justify-center rounded-lg text-xs font-medium transition-colors",
                    cell.disabled && "cursor-not-allowed opacity-25 text-faint",
                    !cell.disabled && !cell.isSelected && "text-ink hover:bg-sunken hover:text-ink",
                    cell.isToday && !cell.isSelected && "border border-accent text-accent font-semibold",
                    cell.isSelected && "bg-accent text-on-accent font-semibold shadow-sm hover:brightness-105"
                  )}
                >
                  {cell.day}
                </button>
              );
            })}
          </div>

          {/* Quick presets & Never expire */}
          <div className="mt-3.5 border-t border-line pt-2.5">
            <div className="mb-2 text-[11px] font-medium uppercase tracking-wider text-faint">
              Quick presets
            </div>
            <div className="grid grid-cols-3 gap-1.5">
              <button
                type="button"
                onClick={() => handleQuickSet(30)}
                className="rounded-md border border-line-strong bg-sunken/60 py-1 text-xs text-muted hover:border-accent hover:bg-accent-soft hover:text-accent-ink transition-colors"
              >
                30 days
              </button>
              <button
                type="button"
                onClick={() => handleQuickSet(90)}
                className="rounded-md border border-line-strong bg-sunken/60 py-1 text-xs text-muted hover:border-accent hover:bg-accent-soft hover:text-accent-ink transition-colors"
              >
                90 days
              </button>
              <button
                type="button"
                onClick={() => handleQuickSet(365)}
                className="rounded-md border border-line-strong bg-sunken/60 py-1 text-xs text-muted hover:border-accent hover:bg-accent-soft hover:text-accent-ink transition-colors"
              >
                1 year
              </button>
            </div>

            <button
              type="button"
              onClick={handleNeverExpire}
              className={cx(
                "mt-2 flex w-full items-center justify-center gap-1.5 rounded-md border py-1.5 text-xs font-medium transition-colors",
                !value
                  ? "border-accent bg-accent-soft text-accent-ink"
                  : "border-line-strong bg-surface text-muted hover:bg-sunken hover:text-ink"
              )}
            >
              <InfinityIcon className="size-3.5" />
              Never expires
            </button>
          </div>
        </div>
      )}
    </div>
  );
}
