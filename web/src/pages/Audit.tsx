import { useCallback, useEffect, useState } from "react";
import { Download, RefreshCw, Search, ShieldCheck } from "lucide-react";
import { api, type AccessKey, type AuditEntry, type AuditFilter } from "../api";
import { dateTime, relative } from "../lib/format";
import { Badge, Button, EmptyState, IconButton, Input, PageHeader, Spinner, cx, errorText, useToast } from "../components/ui";
import { Select } from "../components/Select";

const PAGE = 100;

/** Quotes a CSV field when needed. */
function csvField(v: string | number | undefined): string {
  const s = String(v ?? "");
  return /[",\n]/.test(s) ? `"${s.replace(/"/g, '""')}"` : s;
}

function exportCsv(entries: AuditEntry[]) {
  const cols: (keyof AuditEntry)[] = ["at", "source", "actor", "action", "bucket", "resource", "status", "error", "remote", "requestId"];
  const lines = [cols.join(","), ...entries.map((e) => cols.map((c) => csvField(e[c])).join(","))];
  const href = URL.createObjectURL(new Blob([lines.join("\n") + "\n"], { type: "text/csv" }));
  const a = document.createElement("a");
  a.href = href;
  a.download = `bin-audit-${new Date().toISOString().slice(0, 10)}.csv`;
  document.body.append(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(href), 10_000);
}

export function AuditPage() {
  const [entries, setEntries] = useState<AuditEntry[] | null>(null);
  const [next, setNext] = useState<number | undefined>();
  const [loading, setLoading] = useState(false);
  const [source, setSource] = useState<AuditFilter["source"]>("");
  const [failed, setFailed] = useState(false);
  const [search, setSearch] = useState("");
  const [query, setQuery] = useState(""); // the search actually applied
  const [keys, setKeys] = useState<AccessKey[]>([]);
  const toast = useToast();

  const load = useCallback(
    async (before?: number) => {
      setLoading(true);
      try {
        const page = await api.audit({ source, failed, q: query, before, limit: PAGE });
        setEntries((prev) => (before && prev ? [...prev, ...page.entries] : page.entries));
        setNext(page.nextBefore);
      } catch (e) {
        toast("error", errorText(e));
      } finally {
        setLoading(false);
      }
    },
    [source, failed, query, toast],
  );

  useEffect(() => {
    load();
  }, [load]);
  useEffect(() => {
    api.accessKeys().then(setKeys).catch(() => {});
  }, []);

  // Search as the user pauses typing.
  useEffect(() => {
    const t = setTimeout(() => setQuery(search.trim()), 300);
    return () => clearTimeout(t);
  }, [search]);

  const actorLabel = (actor: string) => keys.find((k) => k.accessKey === actor)?.name;

  return (
    <>
      <PageHeader
        title="Audit log"
        subtitle="Who changed what, from the console and the S3 API. Sign-ins, changes and refused requests are recorded; reads are not."
        actions={
          <>
            <Button icon={<Download className="size-4" />} disabled={!entries?.length} onClick={() => entries && exportCsv(entries)}>
              Export CSV
            </Button>
            <IconButton label="Refresh" onClick={() => load()}>
              <RefreshCw className={cx("size-4", loading && "animate-spin")} />
            </IconButton>
          </>
        }
      />

      <div className="mb-4 flex flex-wrap items-center gap-3">
        <div className="relative w-full max-w-sm">
          <Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-faint" />
          <Input
            type="search"
            name="audit-search"
            aria-label="Search the audit log"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="Search actor, action, bucket or object"
            className="pl-9"
          />
        </div>
        <Select
          aria-label="Source"
          className="w-44"
          value={source ?? ""}
          onChange={(v) => setSource(v as AuditFilter["source"])}
          options={[
            { value: "", label: "All sources" },
            { value: "console", label: "Console" },
            { value: "s3", label: "S3 API" },
          ]}
        />
        <label className="flex items-center gap-2 text-sm">
          <input
            type="checkbox"
            name="failed-only"
            checked={failed}
            onChange={(e) => setFailed(e.target.checked)}
            className="size-4 accent-[var(--accent)]"
          />
          Failures only
        </label>
      </div>

      {entries === null ? (
        <div className="flex justify-center py-20">
          <Spinner />
        </div>
      ) : entries.length === 0 ? (
        <div className="rounded-card border border-dashed border-line-strong bg-surface">
          <EmptyState
            title={query || source || failed ? "Nothing matches" : "Nothing recorded yet"}
            body={
              query || source || failed
                ? "Try another search or clear the filters."
                : "Sign-ins, bucket and object changes and refused requests will appear here."
            }
          />
        </div>
      ) : (
        <div className="overflow-x-auto rounded-card border border-line bg-surface">
          <table className="w-full text-sm">
            <thead className="border-b border-line bg-sunken/60 text-left">
              <tr className="label-strip">
                <th className="py-2 pr-3 pl-4 font-normal whitespace-nowrap">time</th>
                <th className="py-2 pr-3 font-normal">actor</th>
                <th className="py-2 pr-3 font-normal">action</th>
                <th className="py-2 pr-3 font-normal">target</th>
                <th className="py-2 pr-3 font-normal">result</th>
                <th className="hidden py-2 pr-4 font-normal lg:table-cell">client</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-line">
              {entries.map((e) => {
                const ok = e.status < 400;
                const name = actorLabel(e.actor);
                return (
                  <tr key={e.id} className="align-top hover:bg-sunken/60">
                    <td className="py-2.5 pr-3 pl-4 text-xs whitespace-nowrap" title={dateTime(e.at)}>
                      {relative(e.at)}
                    </td>
                    <td className="max-w-48 py-2.5 pr-3">
                      <div className="truncate font-mono text-xs" title={e.actor}>
                        {name ?? e.actor}
                      </div>
                      {name && <div className="truncate font-mono text-[11px] text-faint">{e.actor}</div>}
                    </td>
                    <td className="py-2.5 pr-3">
                      <div className="flex items-center gap-1.5">
                        <Badge tone={e.source === "s3" ? "neutral" : "accent"}>{e.source === "s3" ? "S3" : "console"}</Badge>
                        <span className="font-medium whitespace-nowrap">{e.action}</span>
                      </div>
                    </td>
                    <td className="max-w-0 py-2.5 pr-3">
                      <div className="truncate font-mono text-xs" title={[e.bucket, e.resource].filter(Boolean).join("/")}>
                        {e.bucket && <span className="text-ink">{e.bucket}</span>}
                        {e.bucket && e.resource && <span className="text-faint">/</span>}
                        {e.resource && <span className="text-muted">{e.resource}</span>}
                        {!e.bucket && !e.resource && <span className="text-faint">—</span>}
                      </div>
                    </td>
                    <td className="py-2.5 pr-3 whitespace-nowrap">
                      <Badge tone={ok ? "ok" : e.status === 403 || e.status === 401 ? "warn" : "danger"}>{e.status}</Badge>
                      {e.error && <span className="ml-1.5 font-mono text-[11px] text-muted">{e.error}</span>}
                    </td>
                    <td className="hidden py-2.5 pr-4 font-mono text-[11px] text-muted lg:table-cell" title={e.requestId}>
                      {e.remote}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
          {next && (
            <div className="flex justify-center border-t border-line p-3">
              <Button busy={loading} onClick={() => load(next)}>
                Load older entries
              </Button>
            </div>
          )}
        </div>
      )}

      <p className="mt-4 flex items-center gap-1.5 text-xs text-muted">
        <ShieldCheck className="size-3.5" />
        Entries are kept for the retention set in Configuration → Audit & Logging.
      </p>
    </>
  );
}
