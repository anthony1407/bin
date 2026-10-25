import { useEffect, useRef, useState } from "react";
import { Link } from "react-router";
import { Check, Copy, Download, Pause, Play, Search, Trash2 } from "lucide-react";
import { api, type LogEntry } from "../api";
import { Button, IconButton, Input, cx, errorText, useToast } from "../components/ui";

const LEVELS = ["ALL", "DEBUG", "INFO", "WARN", "ERROR"] as const;
const POLL_MS = 2000;
// Lines kept in the page; the server keeps its own, longer history.
const MAX_LINES = 2000;

function lineText(l: LogEntry): string {
  return `${l.time} ${l.level} ${l.message}${l.attrs ? " " + l.attrs : ""}`;
}

/**
 * The server log, live: the latest lines first, then whatever the server
 * logs next, polled every couple of seconds while the page is visible.
 */
export function LogsPage() {
  const [logs, setLogs] = useState<LogEntry[]>([]);
  const [search, setSearch] = useState("");
  const [filterLevel, setFilterLevel] = useState<(typeof LEVELS)[number]>("ALL");
  const [streaming, setStreaming] = useState(true);
  const [copied, setCopied] = useState(false);
  const [error, setError] = useState("");
  const [follow, setFollow] = useState(true);
  const latest = useRef(0);
  const terminalRef = useRef<HTMLDivElement>(null);
  const toast = useToast();

  useEffect(() => {
    if (!streaming) return;
    let stop = false;
    const poll = async () => {
      if (document.visibilityState !== "visible") return;
      try {
        const page = await api.logs(latest.current);
        if (stop) return;
        setError("");
        if (page.entries.length) {
          setLogs((prev) => [...prev, ...page.entries].slice(-MAX_LINES));
        }
        latest.current = Math.max(latest.current, page.latest);
      } catch (e) {
        if (!stop) setError(errorText(e));
      }
    };
    poll();
    const t = setInterval(poll, POLL_MS);
    return () => {
      stop = true;
      clearInterval(t);
    };
  }, [streaming]);

  // Keep the newest line in view, unless the reader scrolled up.
  useEffect(() => {
    if (follow && terminalRef.current) terminalRef.current.scrollTop = terminalRef.current.scrollHeight;
  }, [logs, follow]);

  const q = search.toLowerCase();
  const filteredLogs = logs.filter(
    (l) => (filterLevel === "ALL" || l.level === filterLevel) && (!q || lineText(l).toLowerCase().includes(q)),
  );

  function copyLogs() {
    navigator.clipboard.writeText(filteredLogs.map(lineText).join("\n")).then(() => {
      setCopied(true);
      toast("ok", `${filteredLogs.length} lines copied.`);
      setTimeout(() => setCopied(false), 1500);
    });
  }

  function downloadLogs() {
    const blob = new Blob([filteredLogs.map(lineText).join("\n") + "\n"], { type: "text/plain" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = `bin-server-${new Date().toISOString().slice(0, 19).replace(/:/g, "-")}.log`;
    a.click();
    URL.revokeObjectURL(url);
  }

  return (
    <div className="space-y-4">
      <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between border-b border-line pb-4">
        <div>
          <h1 className="text-xl font-bold tracking-tight text-ink">Server Logs</h1>
          <p className="text-xs text-muted mt-0.5">
            The server's own log, live. Who changed what is in{" "}
            <Link to="/monitoring/audit" className="text-accent hover:underline">
              Audit
            </Link>
            .
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button icon={streaming ? <Pause className="size-4" /> : <Play className="size-4" />} onClick={() => setStreaming((s) => !s)}>
            {streaming ? "Pause" : "Live"}
          </Button>
          <Button icon={copied ? <Check className="size-4 text-ok" /> : <Copy className="size-4" />} onClick={copyLogs} disabled={!filteredLogs.length}>
            Copy
          </Button>
          <Button icon={<Download className="size-4" />} onClick={downloadLogs} disabled={!filteredLogs.length}>
            Export
          </Button>
          <IconButton label="Clear the view (the server log is kept)" onClick={() => setLogs([])}>
            <Trash2 className="size-4" />
          </IconButton>
        </div>
      </div>

      <div className="flex flex-wrap items-center gap-3">
        <div className="relative flex-1 min-w-56">
          <Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-faint" />
          <Input value={search} onChange={(e) => setSearch(e.target.value)} placeholder="Filter by text, key, IP, request id…" className="pl-9 h-9" />
        </div>
        <div className="flex rounded-md border border-line bg-surface p-0.5 text-xs font-medium">
          {LEVELS.map((lvl) => (
            <button
              key={lvl}
              onClick={() => setFilterLevel(lvl)}
              className={cx(
                "rounded px-2.5 py-1 transition-colors",
                filterLevel === lvl ? "bg-accent-soft text-accent-ink font-semibold" : "text-muted hover:text-ink",
              )}
            >
              {lvl}
            </button>
          ))}
        </div>
        <span className={cx("flex items-center gap-1.5 text-xs", streaming ? "text-ok" : "text-muted")}>
          <span className={cx("size-2 rounded-full", streaming ? "animate-pulse bg-ok" : "bg-line-strong")} />
          {streaming ? "Live" : "Paused"}
        </span>
      </div>

      {error && <div className="rounded-md border border-danger/30 bg-danger-soft px-3 py-2 text-xs text-danger">{error}</div>}

      <div
        ref={terminalRef}
        onScroll={(e) => {
          const el = e.currentTarget;
          setFollow(el.scrollHeight - el.scrollTop - el.clientHeight < 40);
        }}
        className="h-[560px] overflow-auto rounded-xl border border-line-strong bg-[#0f172a] p-4 font-mono text-xs leading-relaxed text-slate-200 shadow-inner"
      >
        {filteredLogs.length === 0 ? (
          <div className="flex h-full items-center justify-center text-slate-500">
            {logs.length === 0 ? "Waiting for log lines…" : "No lines match the filter."}
          </div>
        ) : (
          <div className="space-y-0.5">
            {filteredLogs.map((l) => (
              <div key={l.seq} className="flex items-start gap-2 rounded px-1 py-0.5 hover:bg-white/5">
                <span className="shrink-0 text-slate-500" title={l.time}>
                  {l.time.slice(11, 19)}
                </span>
                <span
                  className={cx(
                    "shrink-0 rounded px-1 text-[10px] font-bold",
                    l.level === "DEBUG" && "bg-slate-800 text-slate-400",
                    l.level === "INFO" && "bg-emerald-950/60 text-emerald-400",
                    l.level === "WARN" && "bg-amber-950/60 text-amber-400",
                    l.level === "ERROR" && "bg-rose-950/60 text-rose-400",
                  )}
                >
                  {l.level}
                </span>
                <span className="break-all">
                  <span className="text-slate-200">{l.message}</span>
                  {l.attrs && <span className="text-slate-500"> {l.attrs}</span>}
                </span>
              </div>
            ))}
          </div>
        )}
      </div>
      {!follow && (
        <button
          type="button"
          onClick={() => setFollow(true)}
          className="text-xs font-medium text-accent hover:underline"
        >
          Jump to the newest line
        </button>
      )}
    </div>
  );
}
