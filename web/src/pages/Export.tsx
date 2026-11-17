import { useCallback, useEffect, useState } from "react";
import { AlertCircle, ArrowUpRight, Pencil, RefreshCw, Trash2, X } from "lucide-react";
import { api, type ExportBucket, type ExportTarget, type ImportJob } from "../api";
import { bytes, count, dateTime, relative } from "../lib/format";
import { Badge, Button, Card, Input, Modal, PasswordInput, Spinner, cx, errorText, useToast } from "../components/ui";

const running = (j: ImportJob) => j.state === "queued" || j.state === "running";

/** Saved exports: buckets of bin sent to another S3 server, and synced again on demand. */
export function SavedExports() {
  const toast = useToast();
  const [targets, setTargets] = useState<ExportTarget[] | null>(null);
  const [editing, setEditing] = useState<ExportTarget | null | undefined>(undefined); // undefined: closed, null: new
  const [deleting, setDeleting] = useState<ExportTarget | null>(null);
  const [deleteBusy, setDeleteBusy] = useState(false);
  const [runningId, setRunningId] = useState("");

  const load = useCallback(() => {
    api.exportTargets().then(setTargets).catch((e) => toast("error", errorText(e)));
  }, [toast]);
  useEffect(load, [load]);
  // While an export is sending, follow it in the list.
  const active = !!targets?.some((t) => t.jobs.some(running));
  useEffect(() => {
    if (!active) return;
    const timer = setInterval(load, 2000);
    return () => clearInterval(timer);
  }, [active, load]);

  async function run(t: ExportTarget) {
    setRunningId(t.id);
    try {
      const res = await api.runExportTarget(t.id);
      toast("ok", `Exporting “${t.name}”: ${count(res.jobs.length)} bucket${res.jobs.length === 1 ? "" : "s"}, only new and changed files are sent.`);
    } catch (e) {
      toast("error", errorText(e));
    } finally {
      setRunningId("");
      load();
    }
  }

  async function remove() {
    if (!deleting) return;
    setDeleteBusy(true);
    try {
      await api.deleteExportTarget(deleting.id);
      toast("ok", `Deleted “${deleting.name}”. What it sent stays on the remote.`);
      setDeleting(null);
      load();
    } catch (e) {
      toast("error", errorText(e));
    } finally {
      setDeleteBusy(false);
    }
  }

  return (
    <section className="space-y-3">
      <div className="flex flex-col justify-between gap-3 sm:flex-row sm:items-end">
        <div>
          <h2 className="text-lg font-semibold text-ink">Saved exports</h2>
          <p className="text-sm text-muted">
            Buckets of bin sent to another server (MinIO, AWS S3, R2…). Missing buckets are created there; sync sends what is
            new or changed. Nothing is deleted on the remote, and the trash is never sent.
          </p>
        </div>
        <Button variant="primary" icon={<ArrowUpRight className="size-4" />} onClick={() => setEditing(null)} className="shrink-0">
          Export to Remote
        </Button>
      </div>
      <Card className="overflow-hidden">
        {targets === null ? (
          <Spinner className="mx-auto my-8" />
        ) : targets.length === 0 ? (
          <div className="p-8 text-center text-sm text-muted">
            No saved exports yet. Use Export to Remote to copy buckets out; the export is saved here to sync again later.
          </div>
        ) : (
          <div className="divide-y divide-line">
            {targets.map((t) => {
              const busy = t.jobs.filter(running);
              const last = t.jobs.filter((j) => j.startedAt >= (t.lastRunAt ?? ""));
              const failed = last.filter((j) => j.state === "failed");
              return (
                <div key={t.id} className="flex flex-wrap items-center gap-3 p-4">
                  <div className="min-w-0 flex-1">
                    <div className="font-medium text-ink">{t.name}</div>
                    <div className="truncate font-mono text-xs text-muted" title={t.endpoint}>
                      {t.endpoint} · {t.allBuckets ? "all buckets" : t.buckets.join(", ")}
                    </div>
                    <div className="mt-0.5 text-xs text-muted">
                      {busy.length > 0 ? (
                        <span className="inline-flex items-center gap-1.5 text-accent">
                          <Spinner className="size-3" /> Sending {count(busy.length)} bucket{busy.length === 1 ? "" : "s"}…
                        </span>
                      ) : (
                        <span title={t.lastRunAt ? dateTime(t.lastRunAt) : undefined}>
                          {t.lastRunAt ? `Last sync ${relative(t.lastRunAt)}` : "Never run"}
                          {last.length > 0 && failed.length === 0 &&
                            ` · ${count(last.reduce((n, j) => n + j.copied, 0))} files sent (${bytes(last.reduce((n, j) => n + j.bytes, 0))})`}
                          {failed.length > 0 && (
                            <span className="text-danger" title={failed.map((j) => `${j.bucket}: ${j.error}`).join("\n")}>
                              {" "}
                              · {failed.length} bucket{failed.length === 1 ? "" : "s"} failed
                            </span>
                          )}
                        </span>
                      )}
                    </div>
                  </div>
                  <div className="flex shrink-0 gap-2">
                    <Button
                      variant="primary"
                      icon={<RefreshCw className={cx("size-4", (busy.length > 0 || runningId === t.id) && "animate-spin")} />}
                      busy={runningId === t.id}
                      disabled={busy.length > 0}
                      onClick={() => run(t)}
                    >
                      Sync now
                    </Button>
                    <Button icon={<Pencil className="size-4" />} onClick={() => setEditing(t)}>
                      Edit
                    </Button>
                    <Button variant="danger" icon={<Trash2 className="size-4" />} onClick={() => setDeleting(t)}>
                      Delete
                    </Button>
                  </div>
                </div>
              );
            })}
          </div>
        )}
      </Card>

      {editing !== undefined && (
        <ExportModal
          target={editing}
          onClose={() => {
            setEditing(undefined);
            load();
          }}
        />
      )}

      <Modal
        open={!!deleting}
        onClose={() => !deleteBusy && setDeleting(null)}
        title="Delete saved export"
        footer={
          <>
            <Button disabled={deleteBusy} onClick={() => setDeleting(null)}>
              Cancel
            </Button>
            <Button variant="danger" busy={deleteBusy} icon={<Trash2 className="size-4" />} onClick={remove}>
              Delete
            </Button>
          </>
        }
      >
        <p className="text-sm">
          Delete <strong>{deleting?.name}</strong>? The buckets and files it sent stay on the remote; only the saved connection
          is removed.
        </p>
      </Modal>
    </section>
  );
}

/** The export wizard: connection (with its name), then buckets, then progress. */
function ExportModal({ target, onClose }: { target: ExportTarget | null; onClose: () => void }) {
  const toast = useToast();
  const [step, setStep] = useState<1 | 2 | 3>(1);
  const [name, setName] = useState(target?.name ?? "");
  const [endpoint, setEndpoint] = useState(target?.endpoint ?? "");
  const [accessKey, setAccessKey] = useState(target?.accessKey ?? "");
  const [secretKey, setSecretKey] = useState("");
  const [region, setRegion] = useState(target?.region || "us-east-1");
  const [local, setLocal] = useState<ExportBucket[]>([]);
  const [all, setAll] = useState(target?.allBuckets ?? false);
  const [selected, setSelected] = useState<Set<string>>(new Set(target?.buckets ?? []));
  const [settings, setSettings] = useState(target?.settings ?? true);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [jobs, setJobs] = useState<ImportJob[]>([]);

  // Follow the started jobs.
  const sending = jobs.some(running);
  useEffect(() => {
    if (step !== 3 || !sending) return;
    const ids = new Set(jobs.map((j) => j.jobId));
    const timer = setInterval(() => {
      api
        .exportJobs()
        .then((list) => setJobs(list.filter((j) => ids.has(j.jobId))))
        .catch(() => {});
    }, 1500);
    return () => clearInterval(timer);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [step, sending]);

  async function connect() {
    const errs: Record<string, string> = {};
    if (!endpoint.trim()) errs.endpoint = "The remote endpoint URL is required.";
    if (!accessKey.trim()) errs.accessKey = "The access key is required.";
    if (!secretKey && !target?.secretKeySet) errs.secretKey = "The secret key is required.";
    setErrors(errs);
    setError("");
    if (Object.keys(errs).length > 0) return;
    setBusy(true);
    try {
      const list = await api.exportTest(endpoint.trim(), accessKey.trim(), secretKey, region, target?.id);
      setLocal(list);
      if (!target) setSelected(new Set(list.map((b) => b.name)));
      if (!name.trim()) {
        try {
          setName(new URL(endpoint.trim()).host);
        } catch {
          /* only a default */
        }
      }
      setStep(2);
    } catch (e) {
      setError(errorText(e));
    } finally {
      setBusy(false);
    }
  }

  async function save(runNow: boolean) {
    if (!name.trim()) {
      setErrors({ name: "Give the export a name." });
      setStep(1);
      return;
    }
    setBusy(true);
    setError("");
    try {
      const input = {
        name: name.trim(),
        endpoint: endpoint.trim(),
        accessKey: accessKey.trim(),
        secretKey,
        region,
        buckets: all ? [] : [...selected],
        allBuckets: all,
        settings,
      };
      const saved = target ? await api.updateExportTarget(target.id, input) : await api.createExportTarget(input);
      if (!runNow) {
        toast("ok", `Saved “${saved.name}”.`);
        onClose();
        return;
      }
      const res = await api.runExportTarget(saved.id);
      setJobs(res.jobs);
      setStep(3);
    } catch (e) {
      setError(errorText(e));
    } finally {
      setBusy(false);
    }
  }

  async function cancel(jobId: string) {
    try {
      await api.cancelExport(jobId);
      toast("ok", "Canceling. Files already sent stay on the remote.");
    } catch (e) {
      toast("error", errorText(e));
    }
  }

  const field = (id: string) => errors[id] && (
    <p className="mt-1 flex items-center gap-1 text-xs font-medium text-danger">
      <AlertCircle className="size-3.5 shrink-0" />
      <span>{errors[id]}</span>
    </p>
  );
  const nothing = !all && selected.size === 0;

  return (
    <Modal
      open
      onClose={() => !busy && onClose()}
      wide
      title={target ? `Edit “${target.name}”` : "Export Buckets to Remote S3"}
      footer={
        step === 1 ? (
          <Button variant="primary" busy={busy} onClick={connect}>
            Connect & Choose Buckets
          </Button>
        ) : step === 2 ? (
          <>
            <Button variant="ghost" onClick={() => setStep(1)}>
              Back
            </Button>
            <Button busy={busy} disabled={nothing} onClick={() => save(false)}>
              Save
            </Button>
            <Button variant="primary" busy={busy} disabled={nothing} onClick={() => save(true)}>
              {all ? "Save & Export All" : `Save & Export (${selected.size})`}
            </Button>
          </>
        ) : (
          <Button onClick={onClose}>Done</Button>
        )
      }
    >
      {step === 1 && (
        <div className="space-y-4">
          <p className="text-sm text-muted">
            The server to send buckets to: MinIO, AWS S3, Cloudflare R2, Wasabi, Backblaze B2 or any S3-compatible one. Its
            credentials need to create buckets and write objects.
          </p>
          <div>
            <label htmlFor="export-name" className="label-strip mb-1.5 block">
              Name
            </label>
            <Input
              id="export-name"
              autoComplete="off"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="e.g. Offsite backup (default: the server's address)"
              aria-invalid={!!errors.name}
            />
            {field("name") || <p className="mt-1 text-xs text-muted">The export is saved under this name, to sync it again later.</p>}
          </div>
          <div>
            <label htmlFor="export-endpoint" className="label-strip mb-1.5 block">
              Remote Endpoint URL <span className="text-danger">*</span>
            </label>
            <Input
              id="export-endpoint"
              autoComplete="url"
              value={endpoint}
              onChange={(e) => setEndpoint(e.target.value)}
              placeholder="https://s3.us-east-1.amazonaws.com or http://minio:9000"
              className="font-mono text-xs"
              aria-invalid={!!errors.endpoint}
            />
            {field("endpoint")}
          </div>
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
            <div>
              <label htmlFor="export-access-key" className="label-strip mb-1.5 block">
                Access Key ID <span className="text-danger">*</span>
              </label>
              <Input
                id="export-access-key"
                autoComplete="username"
                value={accessKey}
                onChange={(e) => setAccessKey(e.target.value)}
                placeholder="AKIA... or minioadmin"
                className="font-mono text-xs"
                aria-invalid={!!errors.accessKey}
              />
              {field("accessKey")}
            </div>
            <div>
              <label htmlFor="export-secret-key" className="label-strip mb-1.5 block">
                Secret Access Key <span className="text-danger">*</span>
              </label>
              <PasswordInput
                id="export-secret-key"
                autoComplete="current-password"
                value={secretKey}
                onChange={(e) => setSecretKey(e.target.value)}
                placeholder={target?.secretKeySet ? "Saved · leave blank to keep it" : "••••••••••••••••"}
                className="font-mono text-xs"
                aria-invalid={!!errors.secretKey}
              />
              {field("secretKey")}
            </div>
          </div>
          <div>
            <label htmlFor="export-region" className="label-strip mb-1.5 block">
              Region
            </label>
            <Input id="export-region" value={region} onChange={(e) => setRegion(e.target.value)} placeholder="us-east-1" className="font-mono text-xs" />
          </div>
          {error && <div className="rounded-md border border-danger bg-danger-soft p-3 text-sm text-danger">{error}</div>}
        </div>
      )}

      {step === 2 && (
        <div className="space-y-4">
          <label className="flex cursor-pointer items-start gap-3 rounded-md border border-line p-3">
            <input type="checkbox" checked={all} onChange={(e) => setAll(e.target.checked)} className="mt-0.5 size-4 accent-[var(--accent)]" />
            <span>
              <span className="block text-sm font-medium text-ink">All buckets</span>
              <span className="block text-xs text-muted">Every bucket of bin, including ones created later, at each sync.</span>
            </span>
          </label>
          {!all && (
            <div className="max-h-72 divide-y divide-line overflow-y-auto rounded-md border border-line">
              {local.length === 0 ? (
                <div className="p-4 text-center text-sm text-muted">There are no buckets to export.</div>
              ) : (
                local.map((b) => (
                  <label key={b.name} className="flex cursor-pointer items-center gap-3 px-3 py-2 text-sm hover:bg-sunken/40">
                    <input
                      type="checkbox"
                      checked={selected.has(b.name)}
                      onChange={() =>
                        setSelected((prev) => {
                          const next = new Set(prev);
                          if (next.has(b.name)) next.delete(b.name);
                          else next.add(b.name);
                          return next;
                        })
                      }
                      className="size-4 accent-[var(--accent)]"
                    />
                    <span className="min-w-0 flex-1 truncate font-mono">{b.name}</span>
                    <span className="shrink-0 text-xs text-muted">
                      {count(b.objects)} objects · {bytes(b.size)}
                    </span>
                    {b.exists ? <Badge tone="accent">on the remote: update</Badge> : <Badge>new</Badge>}
                  </label>
                ))
              )}
            </div>
          )}
          <label className="flex cursor-pointer items-start gap-3">
            <input type="checkbox" checked={settings} onChange={(e) => setSettings(e.target.checked)} className="mt-0.5 size-4 accent-[var(--accent)]" />
            <span>
              <span className="block text-sm font-medium text-ink">Bucket settings too</span>
              <span className="block text-xs text-muted">
                Versioning, tags and anonymous access, for buckets created on the remote. Quotas stay here (each server sets its
                own).
              </span>
            </span>
          </label>
          {error && <div className="rounded-md border border-danger bg-danger-soft p-3 text-sm text-danger">{error}</div>}
        </div>
      )}

      {step === 3 && (
        <div className="space-y-2">
          {jobs.length === 0 ? (
            <p className="text-sm text-muted">Nothing to export.</p>
          ) : (
            jobs.map((j) => (
              <div key={j.jobId} className="rounded-md border border-line p-3 text-sm">
                <div className="flex items-center justify-between gap-3">
                  <span className="font-mono font-medium">{j.bucket}</span>
                  <span className="flex items-center gap-2">
                    {running(j) && <Spinner className="size-3.5" />}
                    <Badge tone={j.state === "done" ? "ok" : j.state === "failed" ? "danger" : "neutral"}>{j.state}</Badge>
                    {running(j) && (
                      <button type="button" onClick={() => cancel(j.jobId)} className="text-muted hover:text-danger" title="Cancel">
                        <X className="size-4" />
                      </button>
                    )}
                  </span>
                </div>
                <div className="mt-1 text-xs text-muted">
                  {count(j.copied)} sent ({bytes(j.bytes)}) · {count(j.unchanged)} already there
                  {j.failed > 0 && <span className="text-danger"> · {count(j.failed)} failed</span>}
                </div>
                {j.error && <div className="mt-1 text-xs text-danger">{j.error}</div>}
                {j.settings && j.settings.length > 0 && <div className="mt-1 text-xs text-muted">Settings sent: {j.settings.join(", ")}</div>}
                {j.notes?.map((n) => (
                  <div key={n} className="mt-1 text-xs text-muted">
                    {n}
                  </div>
                ))}
              </div>
            ))
          )}
        </div>
      )}
    </Modal>
  );
}
