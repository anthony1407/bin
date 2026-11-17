import { useCallback, useEffect, useState } from "react";
import { ArrowDownLeft, ArrowUpRight, RefreshCw } from "lucide-react";
import { api, type ImportJob } from "../api";
import { bytes, count, dateTime, duration, relative } from "../lib/format";
import { Badge, Button, Card, Spinner, cx, errorText, useToast } from "../components/ui";

type Kind = "import" | "export";
type Row = ImportJob & { kind: Kind };
type Filter = "all" | Kind;

const PAGE = 25;
const active = (j: ImportJob) => j.state === "queued" || j.state === "running";

function stateTone(j: ImportJob) {
  switch (j.state) {
    case "done":
      return j.failed > 0 ? "warn" : "ok";
    case "failed":
      return "danger";
    case "canceled":
      return "neutral";
    default:
      return "accent";
  }
}

/** Every import and export job, kept across restarts of bin. */
export function TransferHistory() {
  const toast = useToast();
  const [rows, setRows] = useState<Row[] | null>(null);
  const [filter, setFilter] = useState<Filter>("all");
  const [shown, setShown] = useState(PAGE);
  const [loading, setLoading] = useState(false);

  const load = useCallback(() => {
    setLoading(true);
    Promise.all([api.importJobs(), api.exportJobs()])
      .then(([imports, exports]) => {
        const all: Row[] = [
          ...imports.map((j) => ({ ...j, kind: "import" as const })),
          ...exports.map((j) => ({ ...j, kind: "export" as const })),
        ];
        all.sort((a, b) => Date.parse(b.startedAt) - Date.parse(a.startedAt));
        setRows(all);
      })
      .catch((e) => toast("error", errorText(e)))
      .finally(() => setLoading(false));
  }, [toast]);
  useEffect(load, [load]);
  // Follow jobs while they run.
  const running = !!rows?.some(active);
  useEffect(() => {
    if (!running) return;
    const timer = setInterval(load, 3000);
    return () => clearInterval(timer);
  }, [running, load]);

  const visible = (rows ?? []).filter((r) => filter === "all" || r.kind === filter);

  return (
    <section className="space-y-3">
      <div className="flex flex-col justify-between gap-3 sm:flex-row sm:items-end">
        <div>
          <h2 className="text-lg font-semibold text-ink">Transfer history</h2>
          <p className="text-sm text-muted">
            Every bucket imported or exported, newest first. Kept when bin restarts; a job that was running when bin stopped
            shows as interrupted, and running it again continues where it left off.
          </p>
        </div>
        <div className="flex shrink-0 items-center gap-2">
          <div className="flex rounded-md border border-line-strong p-0.5 text-sm" role="group" aria-label="Show">
            {(["all", "import", "export"] as const).map((f) => (
              <button
                key={f}
                type="button"
                aria-pressed={filter === f}
                onClick={() => {
                  setFilter(f);
                  setShown(PAGE);
                }}
                className={cx(
                  "rounded px-2.5 py-1 transition-colors",
                  filter === f ? "bg-sunken font-medium text-ink" : "text-muted hover:text-ink",
                )}
              >
                {f === "all" ? "All" : f === "import" ? "Imports" : "Exports"}
              </button>
            ))}
          </div>
          <Button icon={<RefreshCw className={cx("size-4", loading && "animate-spin")} />} onClick={load}>
            Refresh
          </Button>
        </div>
      </div>
      <Card className="overflow-hidden">
        {rows === null ? (
          <Spinner className="mx-auto my-8" />
        ) : visible.length === 0 ? (
          <div className="p-8 text-center text-sm text-muted">
            {filter === "all" ? "No imports or exports yet." : filter === "import" ? "No imports yet." : "No exports yet."}
          </div>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead className="border-b border-line bg-sunken/60 text-left">
                <tr className="label-strip">
                  <th className="py-2 pr-3 pl-4 font-normal whitespace-nowrap">started</th>
                  <th className="py-2 pr-3 font-normal">bucket</th>
                  <th className="hidden py-2 pr-3 font-normal md:table-cell">server</th>
                  <th className="py-2 pr-3 font-normal">result</th>
                  <th className="py-2 pr-3 font-normal whitespace-nowrap">files</th>
                  <th className="py-2 pr-4 text-right font-normal">size</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-line">
                {visible.slice(0, shown).map((j) => {
                  const took = j.finishedAt ? (Date.parse(j.finishedAt) - Date.parse(j.startedAt)) / 1000 : null;
                  return (
                    <tr key={j.jobId} className="align-top hover:bg-sunken/60">
                      <td className="py-2.5 pr-3 pl-4 text-xs whitespace-nowrap" title={dateTime(j.startedAt)}>
                        {relative(j.startedAt)}
                        {took !== null && <div className="text-[11px] text-faint">took {duration(Math.max(1, Math.round(took)))}</div>}
                      </td>
                      <td className="max-w-56 py-2.5 pr-3">
                        <div className="flex items-center gap-1.5">
                          {j.kind === "import" ? (
                            <ArrowDownLeft className="size-3.5 shrink-0 text-muted" aria-label="Import" />
                          ) : (
                            <ArrowUpRight className="size-3.5 shrink-0 text-muted" aria-label="Export" />
                          )}
                          <span className="truncate font-mono text-xs text-ink" title={j.bucket}>
                            {j.bucket}
                          </span>
                        </div>
                        <div className="text-[11px] text-faint">{j.kind === "import" ? "Imported" : "Exported"}</div>
                      </td>
                      <td className="hidden max-w-64 py-2.5 pr-3 md:table-cell">
                        <div className="truncate font-mono text-xs text-muted" title={j.endpoint}>
                          {j.endpoint}
                        </div>
                      </td>
                      <td className="max-w-72 py-2.5 pr-3">
                        <span className="inline-flex items-center gap-1.5">
                          {active(j) && <Spinner className="size-3" />}
                          <Badge tone={stateTone(j)}>{j.state}</Badge>
                        </span>
                        {j.error && (
                          <div className="mt-1 line-clamp-2 text-[11px] text-danger" title={j.error}>
                            {j.error}
                          </div>
                        )}
                      </td>
                      <td className="py-2.5 pr-3 text-xs whitespace-nowrap">
                        <div>{count(j.copied)} copied</div>
                        {(j.unchanged > 0 || j.failed > 0) && (
                          <div className="text-[11px] text-faint">
                            {j.unchanged > 0 && `${count(j.unchanged)} unchanged`}
                            {j.unchanged > 0 && j.failed > 0 && " · "}
                            {j.failed > 0 && <span className="text-danger">{count(j.failed)} failed</span>}
                          </div>
                        )}
                      </td>
                      <td className="py-2.5 pr-4 text-right font-mono text-xs whitespace-nowrap">{bytes(j.bytes)}</td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
            {visible.length > shown && (
              <div className="border-t border-line p-3 text-center">
                <Button onClick={() => setShown((n) => n + PAGE)}>
                  Show more ({count(visible.length - shown)} older)
                </Button>
              </div>
            )}
          </div>
        )}
      </Card>
    </section>
  );
}
