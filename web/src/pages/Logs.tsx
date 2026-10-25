import { useRef, useState } from "react";
import { Link } from "react-router";
import { Copy, Download, Play, Search, Trash2 } from "lucide-react";
import { Button, ComingSoon, IconButton, Input, cx } from "../components/ui";

interface LogEntry {
  id: string;
  timestamp: string;
  level: "INFO" | "WARN" | "ERROR";
  source: string;
  message: string;
}

const MOCK_INITIAL_LOGS: LogEntry[] = [
  {
    id: "1",
    timestamp: new Date(Date.now() - 120000).toISOString(),
    level: "INFO",
    source: "s3.server",
    message: "API server listening on 127.0.0.1:9000 (HTTP/1.1)",
  },
  {
    id: "2",
    timestamp: new Date(Date.now() - 95000).toISOString(),
    level: "INFO",
    source: "auth.keyring",
    message: "Loaded access keys from storage engine",
  },
  {
    id: "3",
    timestamp: new Date(Date.now() - 60000).toISOString(),
    level: "INFO",
    source: "s3.handler",
    message: "GET /images?list-type=2 HTTP/1.1 status=200 duration=2.1ms",
  },
  {
    id: "4",
    timestamp: new Date(Date.now() - 45000).toISOString(),
    level: "INFO",
    source: "s3.handler",
    message: "PUT /images/avatar.png HTTP/1.1 status=200 bytes=42194 duration=8.4ms",
  },
  {
    id: "5",
    timestamp: new Date(Date.now() - 20000).toISOString(),
    level: "INFO",
    source: "console.admin",
    message: "Admin session authenticated from 127.0.0.1",
  },
];

// The live server log is not wired to the console yet: this page shows a
// few sample lines so the layout can be reviewed, and says so.
export function LogsPage() {
  const [logs, setLogs] = useState<LogEntry[]>(MOCK_INITIAL_LOGS);
  const [search, setSearch] = useState("");
  const [filterLevel, setFilterLevel] = useState<string>("ALL");
  const terminalRef = useRef<HTMLDivElement>(null);

  const filteredLogs = logs.filter((l) => {
    if (filterLevel !== "ALL" && l.level !== filterLevel) return false;
    if (search && !l.message.toLowerCase().includes(search.toLowerCase()) && !l.source.toLowerCase().includes(search.toLowerCase())) {
      return false;
    }
    return true;
  });

  return (
    <div className="space-y-4">
      <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between border-b border-line pb-4">
        <div>
          <h1 className="text-xl font-bold tracking-tight text-ink">Server Logs</h1>
          <p className="text-xs text-muted mt-0.5">S3 API and administrative activity logs</p>
        </div>
        <div className="flex items-center gap-2">
          <Button icon={<Play className="size-4" />} disabled>
            Live Stream
          </Button>
          <Button icon={<Copy className="size-4" />} disabled>
            Copy
          </Button>
          <Button icon={<Download className="size-4" />} disabled>
            Export
          </Button>
          <IconButton label="Clear logs" onClick={() => setLogs([])}>
            <Trash2 className="size-4" />
          </IconButton>
        </div>
      </div>

      <ComingSoon>
        Live logs in the console are planned. The lines below are samples, not your server's activity. Today, read the
        log with <code className="font-mono">docker compose logs -f</code> (or the process output), and see who changed
        what in <Link to="/monitoring/audit" className="font-semibold underline">Monitoring → Audit</Link>.
      </ComingSoon>

      {/* Filter Toolbar */}
      <div className="flex flex-wrap items-center gap-3">
        <div className="relative flex-1 min-w-56">
          <Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-faint" />
          <Input
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="Filter logs by message or source…"
            className="pl-9 h-9"
          />
        </div>
        <div className="flex rounded-md border border-line bg-surface p-0.5 text-xs font-medium">
          {["ALL", "INFO", "WARN", "ERROR"].map((lvl) => (
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
      </div>

      {/* Log Console Terminal View */}
      <div
        ref={terminalRef}
        className="h-[560px] overflow-auto rounded-xl border border-line-strong bg-[#0f172a] p-4 font-mono text-xs leading-relaxed text-slate-200 shadow-inner"
      >
        {filteredLogs.length === 0 ? (
          <div className="flex h-full items-center justify-center text-slate-500">
            No logs matching filter.
          </div>
        ) : (
          <div className="space-y-1">
            {filteredLogs.map((l) => (
              <div key={l.id} className="flex items-start gap-2 hover:bg-white/5 px-1 py-0.5 rounded">
                <span className="shrink-0 text-slate-500">{l.timestamp.slice(11, 23)}</span>
                <span
                  className={cx(
                    "shrink-0 font-bold px-1 rounded text-[10px]",
                    l.level === "INFO" && "text-emerald-400 bg-emerald-950/60",
                    l.level === "WARN" && "text-amber-400 bg-amber-950/60",
                    l.level === "ERROR" && "text-rose-400 bg-rose-950/60",
                  )}
                >
                  {l.level}
                </span>
                <span className="shrink-0 text-indigo-300 font-medium">{l.source}:</span>
                <span className="break-all text-slate-300">{l.message}</span>
                <span className="ml-auto shrink-0 text-[10px] text-slate-600">sample</span>
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}
