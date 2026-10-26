import { useState, useEffect, useCallback } from "react";
import {
  AlertCircle,
  ArrowDownLeft,
  Check,
  CheckCircle2,
  CircleAlert,
  Copy,
  Download,
  KeyRound,
  Pencil,
  RefreshCw,
  Trash2,
} from "lucide-react";
import {
  api,
  ApiError,
  type Bucket,
  type ImportIAMResult,
  type ImportIAMSummary,
  type ImportJob,
  type ImportSkipped,
  type ImportSource,
  type RemoteBucket,
} from "../api";
import { PageHeader, Button, Card, Input, Modal, PasswordInput, Spinner, cx, errorText, useToast } from "../components/ui";
import { ReplicationRules } from "./ReplicationRules";
import { bytes, count, dateTime, relative } from "../lib/format";

export function ReplicationPage() {
  const [buckets, setBuckets] = useState<Bucket[]>([]);

  // Import wizard state
  const [importModal, setImportModal] = useState(false);
  const [importStep, setImportStep] = useState<1 | 2 | 3>(1);
  const [importEndpoint, setImportEndpoint] = useState("");
  const [importAccessKey, setImportAccessKey] = useState("");
  const [importSecretKey, setImportSecretKey] = useState("");
  const [importRegion, setImportRegion] = useState("us-east-1");
  const [remoteBuckets, setRemoteBuckets] = useState<RemoteBucket[]>([]);
  const [selectedRemoteBuckets, setSelectedRemoteBuckets] = useState<Set<string>>(new Set());
  const [importLoading, setImportLoading] = useState(false);
  const [importError, setImportError] = useState("");
  const [importErrors, setImportErrors] = useState<Record<string, string>>({});
  const [importJobs, setImportJobs] = useState<ImportJob[]>([]);
  const [importSkipped, setImportSkipped] = useState<ImportSkipped[]>([]);
  // What else comes along: bucket settings, and the remote's identities (MinIO).
  const [remoteIam, setRemoteIam] = useState<ImportIAMSummary | null>(null);
  const [withSettings, setWithSettings] = useState(true);
  const [withIam, setWithIam] = useState(false);
  const [iamResult, setIamResult] = useState<ImportIAMResult | null>(null);
  const [iamError, setIamError] = useState("");
  // Imports are saved under a name, to be run again, edited or deleted.
  const [importName, setImportName] = useState("");
  const [editing, setEditing] = useState<ImportSource | null>(null);
  const [sources, setSources] = useState<ImportSource[] | null>(null);
  const [deleting, setDeleting] = useState<ImportSource | null>(null);
  const [deleteBusy, setDeleteBusy] = useState(false);
  const [runningSource, setRunningSource] = useState("");

  const toast = useToast();

  const loadSources = useCallback(() => {
    api.importSources().then(setSources).catch((e) => toast("error", errorText(e)));
  }, [toast]);
  useEffect(loadSources, [loadSources]);
  // While a saved import is copying, follow it in the list.
  const sourcesActive = !!sources?.some((s) => s.jobs.some((j) => j.state === "queued" || j.state === "running"));
  useEffect(() => {
    if (!sourcesActive) return;
    const timer = setInterval(loadSources, 2000);
    return () => clearInterval(timer);
  }, [sourcesActive, loadSources]);

  // While the import modal shows started jobs, follow their progress.
  const importActive = importJobs.some((j) => j.state === "queued" || j.state === "running");
  useEffect(() => {
    if (!importModal || importStep !== 3 || !importActive) return;
    const ids = new Set(importJobs.map((j) => j.jobId));
    const timer = setInterval(() => {
      api
        .importJobs()
        .then((all) => {
          setImportJobs(all.filter((j) => ids.has(j.jobId)));
          if (!all.some((j) => ids.has(j.jobId) && (j.state === "queued" || j.state === "running"))) {
            api.buckets().then(setBuckets).catch(() => {});
          }
        })
        .catch(() => {});
    }, 1500);
    return () => clearInterval(timer);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [importModal, importStep, importActive]);


  useEffect(() => {
    api.buckets().then((list) => {
      setBuckets(list);
    }).catch(() => {});
  }, []);


  function clearImportError(field: string) {
    if (importErrors[field]) {
      setImportErrors((prev) => {
        const next = { ...prev };
        delete next[field];
        return next;
      });
    }
  }


  // Import wizard functions; src opens a saved import for editing.
  function openImportModal(src?: ImportSource) {
    setEditing(src ?? null);
    setImportName(src?.name ?? "");
    setImportStep(1);
    setImportEndpoint(src?.endpoint ?? "");
    setImportAccessKey(src?.accessKey ?? "");
    setImportSecretKey("");
    setImportRegion(src?.region || "us-east-1");
    setRemoteBuckets([]);
    setSelectedRemoteBuckets(new Set());
    setImportError("");
    setImportErrors({});
    setImportJobs([]);
    setImportSkipped([]);
    setRemoteIam(null);
    setWithSettings(true);
    setWithIam(false);
    setIamResult(null);
    setIamError("");
    setImportModal(true);
  }

  async function testImportConnection() {
    setImportError("");
    setImportErrors({});
    setImportLoading(true);

    const errs: Record<string, string> = {};
    if (!importEndpoint.trim()) {
      errs.importEndpoint = "Remote Endpoint URL is required.";
    }
    if (!importAccessKey.trim()) {
      errs.importAccessKey = "Remote Access Key ID is required.";
    }
    if (!importSecretKey.trim() && !editing?.secretKeySet) {
      errs.importSecretKey = "Remote Secret Key is required.";
    }

    if (Object.keys(errs).length > 0) {
      setImportErrors(errs);
      setImportLoading(false);
      return;
    }

    try {
      const { buckets: remote, iam } = await api.importTest(importEndpoint, importAccessKey, importSecretKey, importRegion, editing?.id);
      setRemoteBuckets(remote);
      if (editing) {
        // Keep the saved choices.
        setSelectedRemoteBuckets(new Set(remote.map((b) => b.name).filter((n) => editing.buckets.includes(n))));
        setWithSettings(editing.settings);
        setWithIam(editing.iam && iam.available);
      } else {
        // Buckets that already exist here get only what is new, so all start selected.
        setSelectedRemoteBuckets(new Set(remote.map((b) => b.name)));
        setWithIam(false);
        if (!importName.trim()) {
          try {
            setImportName(new URL(importEndpoint.trim()).host);
          } catch {
            /* the server already accepted it; a name is only a default */
          }
        }
      }
      setRemoteIam(iam);
      setImportStep(2);
    } catch (e: any) {
      setImportError(e.message || "Failed to connect to remote server");
    } finally {
      setImportLoading(false);
    }
  }

  function toggleBucketSelection(bucketName: string) {
    setSelectedRemoteBuckets((prev) => {
      const next = new Set(prev);
      if (next.has(bucketName)) next.delete(bucketName);
      else next.add(bucketName);
      return next;
    });
  }

  // saveImport saves the import under its name and, with run, runs it.
  async function saveImport(run: boolean) {
    if (!importName.trim()) {
      setImportErrors({ importName: "Give the import a name." });
      return;
    }
    setImportLoading(true);
    setImportError("");
    setImportJobs([]);
    setImportSkipped([]);
    setIamResult(null);
    setIamError("");

    try {
      const input = {
        name: importName.trim(),
        endpoint: importEndpoint.trim(),
        accessKey: importAccessKey.trim(),
        secretKey: importSecretKey,
        region: importRegion,
        buckets: Array.from(selectedRemoteBuckets),
        settings: withSettings,
        iam: withIam,
      };
      const saved = editing ? await api.updateImportSource(editing.id, input) : await api.createImportSource(input);
      setEditing(saved);
      loadSources();
      if (!run) {
        toast("ok", `Saved “${saved.name}”.`);
        setImportModal(false);
        return;
      }
      const res = await api.runImportSource(saved.id);
      loadSources();
      api.buckets().then(setBuckets).catch(() => {});
      setImportJobs(res.jobs);
      setImportSkipped(res.skipped);
      setIamResult(res.iam ?? null);
      setIamError(res.iamError ?? "");
      setImportStep(3);
      if (res.jobs.length > 0) toast("ok", `Import started for ${res.jobs.length} bucket(s).`);
    } catch (e) {
      if (e instanceof ApiError && Array.isArray(e.data.skipped)) {
        setImportSkipped(e.data.skipped as ImportSkipped[]);
        setImportStep(3);
      } else {
        setImportError(e instanceof Error ? e.message : "Import failed");
      }
    } finally {
      setImportLoading(false);
    }
  }

  // runSource runs a saved import from the list; what is new comes along.
  async function runSource(src: ImportSource) {
    setRunningSource(src.id);
    try {
      const res = await api.runImportSource(src.id);
      const notes = [res.iamError, ...res.skipped.map((s) => `${s.bucket}: ${s.reason}`)].filter(Boolean);
      toast(notes.length ? "error" : "ok", notes.length ? notes.join(" ") : `Syncing “${src.name}”: only new and changed files are copied.`);
      if (res.iam?.keys.length) {
        // New secrets exist only in this answer: show them.
        openImportModal(src);
        setIamResult(res.iam);
        setImportJobs(res.jobs);
        setImportStep(3);
      }
    } catch (e) {
      toast("error", errorText(e));
    } finally {
      setRunningSource("");
      loadSources();
    }
  }

  async function deleteSource() {
    if (!deleting) return;
    setDeleteBusy(true);
    try {
      await api.deleteImportSource(deleting.id);
      toast("ok", `Deleted “${deleting.name}”. What it imported stays.`);
      setDeleting(null);
      loadSources();
    } catch (e) {
      toast("error", errorText(e));
    } finally {
      setDeleteBusy(false);
    }
  }

  async function cancelImport(jobId: string) {
    try {
      // The job stops at the next object; polling then shows it as canceled.
      await api.cancelImport(jobId);
      toast("ok", "Canceling the import. Objects already copied are kept.");
    } catch (e) {
      toast("error", e instanceof Error ? e.message : "Could not cancel the import");
    }
  }

  function closeImportModal() {
    setImportModal(false);
  }


  function ImportFooter() {
    if (importStep === 1) {
      return (
        <Button variant="primary" busy={importLoading} onClick={testImportConnection}>
          Connect & List Buckets
        </Button>
      );
    }
    if (importStep === 2) {
      return (
        <>
          <Button variant="ghost" onClick={() => { setImportStep(1); setImportErrors({}); setImportError(""); }}>
            Back
          </Button>
          {editing && (
            <Button busy={importLoading} disabled={selectedRemoteBuckets.size === 0 && !withIam} onClick={() => saveImport(false)}>
              Save
            </Button>
          )}
          <Button
            variant="primary"
            busy={importLoading}
            disabled={selectedRemoteBuckets.size === 0 && !withIam}
            onClick={() => saveImport(true)}
          >
            {editing ? "Save & Sync" : selectedRemoteBuckets.size === 0 && withIam ? "Save & Import Users" : `Save & Import (${selectedRemoteBuckets.size})`}
          </Button>
        </>
      );
    }
    if (importStep === 3) {
      return <Button onClick={closeImportModal}>Done</Button>;
    }
    return null;
  }

  return (
    <div className="space-y-6 pb-16">
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4">
        <PageHeader
          title="Bucket Replication & Backup"
          subtitle="Keep buckets in step with AWS S3, Cloudflare R2, Wasabi, Backblaze B2 or any S3-compatible server, or copy whole buckets in once."
        />
        <Button
          variant="primary"
          icon={<ArrowDownLeft className="size-4" />}
          onClick={() => openImportModal()}
          className="shrink-0"
        >
          Import from Remote
        </Button>
      </div>

      <section className="space-y-3">
        <div>
          <h2 className="text-lg font-semibold text-ink">Saved imports</h2>
          <p className="text-sm text-muted">
            Buckets copied in from another server. Sync brings what is new or changed there; nothing is deleted here.
          </p>
        </div>
        <Card className="overflow-hidden">
          {sources === null ? (
            <Spinner className="mx-auto my-8" />
          ) : sources.length === 0 ? (
            <div className="p-8 text-center text-sm text-muted">
              No saved imports yet. Use Import from Remote to copy buckets in; the import is saved here to sync again later.
            </div>
          ) : (
            <div className="divide-y divide-line">
              {sources.map((s) => {
                const active = s.jobs.filter((j) => j.state === "queued" || j.state === "running");
                const last = s.jobs.filter((j) => j.startedAt >= (s.lastRunAt ?? ""));
                const failed = last.some((j) => j.state === "failed");
                return (
                  <div key={s.id} className="flex flex-wrap items-center gap-3 p-4">
                    <div className="min-w-0 flex-1">
                      <div className="font-medium text-ink">{s.name}</div>
                      <div className="truncate font-mono text-xs text-muted" title={s.endpoint}>
                        {s.endpoint} · {s.buckets.length > 0 ? s.buckets.join(", ") : "no buckets"}
                        {s.iam && " · users & policies"}
                      </div>
                      <div className="mt-0.5 text-xs text-muted">
                        {active.length > 0 ? (
                          <span className="inline-flex items-center gap-1.5 text-accent">
                            <Spinner className="size-3" /> Syncing {count(active.length)} bucket{active.length === 1 ? "" : "s"}…
                          </span>
                        ) : (
                          <span title={s.lastRunAt ? dateTime(s.lastRunAt) : undefined}>
                            Last sync {relative(s.lastRunAt)}
                            {last.length > 0 && !failed && ` · ${count(last.reduce((n, j) => n + j.copied, 0))} new or changed files`}
                            {failed && <span className="text-danger"> · some buckets failed</span>}
                          </span>
                        )}
                      </div>
                    </div>
                    <div className="flex shrink-0 gap-2">
                      <Button
                        variant="primary"
                        icon={<RefreshCw className={cx("size-4", (active.length > 0 || runningSource === s.id) && "animate-spin")} />}
                        busy={runningSource === s.id}
                        disabled={active.length > 0}
                        onClick={() => runSource(s)}
                      >
                        Sync now
                      </Button>
                      <Button icon={<Pencil className="size-4" />} onClick={() => openImportModal(s)}>
                        Edit
                      </Button>
                      <Button variant="danger" icon={<Trash2 className="size-4" />} onClick={() => setDeleting(s)}>
                        Delete
                      </Button>
                    </div>
                  </div>
                );
              })}
            </div>
          )}
        </Card>
      </section>

      <ReplicationRules buckets={buckets} />

      <Modal
        open={!!deleting}
        onClose={() => !deleteBusy && setDeleting(null)}
        title="Delete saved import"
        footer={
          <>
            <Button disabled={deleteBusy} onClick={() => setDeleting(null)}>
              Cancel
            </Button>
            <Button variant="danger" busy={deleteBusy} icon={<Trash2 className="size-4" />} onClick={deleteSource}>
              Delete
            </Button>
          </>
        }
      >
        <p className="text-sm">
          Delete <strong>{deleting?.name}</strong>? Buckets, files, users and groups it imported stay; only the saved
          connection is removed.
        </p>
      </Modal>


      {/* Import from Remote Wizard Modal */}
      <Modal
        open={importModal}
        onClose={closeImportModal}
        title={editing ? `Edit “${editing.name}”` : "Import Buckets from Remote S3"}
        wide
        footer={<ImportFooter />}
      >
        {importStep === 1 && (
          <div className="space-y-4">
            <p className="text-sm text-muted">
              Enter the connection details for the remote S3-compatible server (MinIO, AWS S3,
              Cloudflare R2, Wasabi, Backblaze B2, etc.) to list available buckets.
            </p>

            <div>
              <label htmlFor="import-endpoint" className="label-strip mb-1.5 block">
                Remote Endpoint URL <span className="text-danger">*</span>
              </label>
              <Input
                id="import-endpoint"
                autoComplete="url"
                value={importEndpoint}
                onChange={(e) => {
                  setImportEndpoint(e.target.value);
                  clearImportError("importEndpoint");
                }}
                placeholder="https://s3.us-east-1.amazonaws.com or http://minio:9000"
                className={cx("font-mono text-xs", importErrors.importEndpoint && "!border-danger focus:!border-danger focus:!ring-danger/25")}
              />
              {importErrors.importEndpoint && (
                <p className="mt-1 text-xs text-danger font-medium flex items-center gap-1">
                  <AlertCircle className="size-3.5 shrink-0" />
                  <span>{importErrors.importEndpoint}</span>
                </p>
              )}
            </div>

            <div className="grid grid-cols-2 gap-3">
              <div>
                <label htmlFor="import-access-key" className="label-strip mb-1.5 block">
                  Access Key ID <span className="text-danger">*</span>
                </label>
                <Input
                  id="import-access-key"
                  autoComplete="username"
                  value={importAccessKey}
                  onChange={(e) => {
                    setImportAccessKey(e.target.value);
                    clearImportError("importAccessKey");
                  }}
                  placeholder="AKIA... or minioadmin"
                  className={cx("font-mono text-xs", importErrors.importAccessKey && "!border-danger focus:!border-danger focus:!ring-danger/25")}
                />
                {importErrors.importAccessKey && (
                  <p className="mt-1 text-xs text-danger font-medium flex items-center gap-1">
                    <AlertCircle className="size-3.5 shrink-0" />
                    <span>{importErrors.importAccessKey}</span>
                  </p>
                )}
              </div>
              <div>
                <label htmlFor="import-secret-key" className="label-strip mb-1.5 block">
                  Secret Access Key <span className="text-danger">*</span>
                </label>
                <PasswordInput
                  id="import-secret-key"
                  autoComplete="current-password"
                  value={importSecretKey}
                  onChange={(e) => {
                    setImportSecretKey(e.target.value);
                    clearImportError("importSecretKey");
                  }}
                  placeholder={editing?.secretKeySet ? "Saved · leave blank to keep it" : "••••••••••••••••"}
                  className={cx("font-mono text-xs", importErrors.importSecretKey && "!border-danger focus:!border-danger focus:!ring-danger/25")}
                />
                {importErrors.importSecretKey && (
                  <p className="mt-1 text-xs text-danger font-medium flex items-center gap-1">
                    <AlertCircle className="size-3.5 shrink-0" />
                    <span>{importErrors.importSecretKey}</span>
                  </p>
                )}
              </div>
            </div>

            <div>
              <label htmlFor="import-region" className="label-strip mb-1.5 block">Region</label>
              <Input
                id="import-region"
                autoComplete="off"
                value={importRegion}
                onChange={(e) => setImportRegion(e.target.value)}
                placeholder="us-east-1"
                className="font-mono text-xs"
              />
            </div>

            {importError && (
              <div className="p-3 rounded-md bg-danger-soft border border-danger text-danger text-sm">
                {importError}
              </div>
            )}
          </div>
        )}

        {importStep === 2 && (
          <div className="space-y-4">
            <div>
              <label htmlFor="import-name" className="label-strip mb-1.5 block">
                Name <span className="text-danger">*</span>
              </label>
              <Input
                id="import-name"
                autoComplete="off"
                value={importName}
                onChange={(e) => {
                  setImportName(e.target.value);
                  clearImportError("importName");
                }}
                placeholder="e.g. Old MinIO"
                aria-invalid={!!importErrors.importName}
              />
              {importErrors.importName ? (
                <p className="mt-1 flex items-center gap-1 text-xs font-medium text-danger">
                  <AlertCircle className="size-3.5 shrink-0" />
                  <span>{importErrors.importName}</span>
                </p>
              ) : (
                <p className="mt-1 text-xs text-muted">The import is saved under this name, to sync it again later.</p>
              )}
            </div>

            <p className="text-sm text-muted">
              Select the buckets to import. Each bucket is created here and its objects are copied from the
              remote server. Importing a bucket again copies only the files that are new or changed there.
            </p>

            {importError && (
              <div className="p-3 rounded-md bg-danger-soft border border-danger text-danger text-sm">
                {importError}
              </div>
            )}

            <div className="rounded-lg border border-line overflow-hidden">
              <div className="p-3 bg-surface-alt border-b border-line flex items-center gap-2">
                <input
                  type="checkbox"
                  checked={selectedRemoteBuckets.size === remoteBuckets.length && remoteBuckets.length > 0}
                  onChange={(e) =>
                    e.target.checked
                      ? setSelectedRemoteBuckets(new Set(remoteBuckets.map((b) => b.name)))
                      : setSelectedRemoteBuckets(new Set())
                  }
                  className="size-4 accent-[var(--accent)]"
                />
                <span className="font-medium text-xs">Select All</span>
              </div>
              {remoteBuckets.map((bucket) => (
                <label
                  key={bucket.name}
                  className="flex items-center gap-3 p-3 hover:bg-sunken transition-colors cursor-pointer border-b border-line last:border-0"
                >
                  <input
                    type="checkbox"
                    checked={selectedRemoteBuckets.has(bucket.name)}
                    onChange={() => toggleBucketSelection(bucket.name)}
                    className="size-4 accent-[var(--accent)]"
                  />
                  <div className="flex-1 min-w-0">
                    <div className="font-mono text-sm font-medium text-ink truncate">{bucket.name}</div>
                    <div className="flex gap-4 text-xs text-muted font-mono mt-0.5">
                      <span>{bucket.partial ? "≥ " : ""}{bytes(bucket.size)}</span>
                      <span>{bucket.partial ? "≥ " : ""}{count(bucket.objects)} objects</span>
                    </div>
                  </div>
                  {buckets.some((b) => b.name === bucket.name) && (
                    <span className="text-xs text-accent bg-accent-soft px-2 py-0.5 rounded" title="Only new and changed files are copied">
                      Exists here · update
                    </span>
                  )}
                </label>
              ))}
              {remoteBuckets.length === 0 && !importLoading && (
                <div className="p-8 text-center text-muted">No buckets found on remote server</div>
              )}
            </div>

            <div className="space-y-3 rounded-lg border border-line p-3">
              <div className="label-strip">Also import</div>
              <label className="flex cursor-pointer items-start gap-3">
                <input
                  type="checkbox"
                  checked={withSettings}
                  onChange={(e) => setWithSettings(e.target.checked)}
                  className="mt-0.5 size-4 accent-[var(--accent)]"
                />
                <div>
                  <div className="text-sm font-medium text-ink">Bucket settings</div>
                  <div className="text-xs text-muted">Versioning, tags, anonymous (public) access and quota of each selected bucket.</div>
                </div>
              </label>
              <label className={cx("flex items-start gap-3", remoteIam?.available ? "cursor-pointer" : "cursor-not-allowed opacity-70")}>
                <input
                  type="checkbox"
                  checked={withIam}
                  disabled={!remoteIam?.available}
                  onChange={(e) => setWithIam(e.target.checked)}
                  className="mt-0.5 size-4 accent-[var(--accent)]"
                />
                <div>
                  <div className="text-sm font-medium text-ink">Users, groups, policies and access keys</div>
                  {remoteIam?.available ? (
                    <div className="text-xs text-muted">
                      {count(remoteIam.users)} user{remoteIam.users === 1 ? "" : "s"} · {count(remoteIam.groups)} group
                      {remoteIam.groups === 1 ? "" : "s"} · {count(remoteIam.policies)} custom polic{remoteIam.policies === 1 ? "y" : "ies"} ·{" "}
                      {count(remoteIam.accessKeys)} access key{remoteIam.accessKeys === 1 ? "" : "s"}. Policies become groups here.
                      MinIO never reveals secrets, so every user and access key gets a new secret key, shown once.
                    </div>
                  ) : (
                    <div className="text-xs text-muted">{remoteIam?.reason ?? "Only available from MinIO servers."}</div>
                  )}
                </div>
              </label>
            </div>
          </div>
        )}

        {importStep === 3 && (
          <div className="space-y-4">
            <div
              className={cx(
                "flex items-center gap-3 p-3 rounded-lg border",
                importActive ? "bg-accent-soft border-accent/20" : "bg-ok/10 border-ok/20",
              )}
            >
              {importActive ? (
                <Spinner className="size-5 text-accent" />
              ) : (
                <CheckCircle2 className="size-5 text-ok shrink-0" />
              )}
              <div>
                <div className="font-semibold text-ink">{importActive ? "Importing…" : "Import finished"}</div>
                {importJobs.length + importSkipped.length > 0 && (
                  <div className="text-xs text-muted">
                    {importJobs.filter((j) => j.state === "done").length} of {importJobs.length + importSkipped.length}{" "}
                    bucket(s) imported. You can close this window; the copy continues on the server.
                  </div>
                )}
              </div>
            </div>

            <div className="space-y-2 max-h-60 overflow-y-auto">
              {importJobs.map((job) => {
                const active = job.state === "queued" || job.state === "running";
                return (
                  <div key={job.jobId} className="flex items-center gap-3 p-3 rounded-lg bg-surface border border-line">
                    {job.state === "done" ? (
                      <CheckCircle2 className="size-5 text-ok shrink-0" />
                    ) : active ? (
                      <Spinner className="size-5 text-accent" />
                    ) : (
                      <CircleAlert className="size-5 text-danger shrink-0" />
                    )}
                    <div className="flex-1 min-w-0">
                      <div className="font-mono text-sm font-medium text-ink">{job.bucket}</div>
                      <div className="text-xs text-muted mt-0.5">
                        {job.state === "queued"
                          ? "Waiting to start"
                          : `${count(job.copied)} ${job.update ? "new or changed " : ""}objects · ${bytes(job.bytes)}`}
                        {job.update && job.unchanged > 0 && ` · ${count(job.unchanged)} unchanged`}
                        {job.failed > 0 && <span className="text-danger"> · {count(job.failed)} failed</span>}
                        {job.state === "canceled" && " · canceled"}
                      </div>
                      {job.error && <div className="text-xs text-danger mt-0.5">{job.error}</div>}
                      {!!job.settings?.length && (
                        <div className="text-xs text-ok mt-0.5">Settings: {job.settings.join(", ")}</div>
                      )}
                      {job.notes?.map((n) => (
                        <div key={n} className="text-xs text-warning mt-0.5">{n}</div>
                      ))}
                    </div>
                    {active && (
                      <Button variant="ghost" onClick={() => cancelImport(job.jobId)}>
                        Cancel
                      </Button>
                    )}
                  </div>
                );
              })}
              {importSkipped.map((s) => (
                <div key={s.bucket} className="flex items-center gap-3 p-3 rounded-lg bg-surface border border-line">
                  <CircleAlert className="size-5 text-warning shrink-0" />
                  <div className="flex-1 min-w-0">
                    <div className="font-mono text-sm font-medium text-ink">{s.bucket}</div>
                    <div className="text-xs text-warning mt-0.5">Skipped: {s.reason}</div>
                  </div>
                </div>
              ))}
            </div>

            {importJobs.some((j) => j.failed > 0) && (
              <div className="p-3 rounded-md bg-warning-soft border border-warning text-warning text-sm">
                Some objects could not be copied; the server log lists each one.
              </div>
            )}

            {iamError && (
              <div className="p-3 rounded-md bg-danger-soft border border-danger text-danger text-sm">
                Users and policies were not imported: {iamError}
              </div>
            )}
            {iamResult && <IAMReport result={iamResult} />}
          </div>
        )}
      </Modal>
    </div>
  );
}

const kindLabels = { policy: "Policy", group: "Group", user: "User", accessKey: "Access key" } as const;

// IAMReport shows what the import made of the remote's identities. The new
// secret keys exist only here, so they can be copied or saved as CSV.
function IAMReport({ result }: { result: ImportIAMResult }) {
  const [copied, setCopied] = useState("");
  const copy = (text: string, id: string) => {
    navigator.clipboard.writeText(text).then(() => {
      setCopied(id);
      setTimeout(() => setCopied(""), 1500);
    });
  };
  const downloadCSV = () => {
    const quote = (s: string) => `"${s.replaceAll('"', '""')}"`;
    const rows = [
      ["access_key", "secret_key", "kind", "user", "enabled", "groups"],
      ...result.keys.map((k) => [k.accessKey, k.secretKey, k.kind, k.parent ?? "", String(k.enabled), k.groups.join(" ")]),
    ];
    const url = URL.createObjectURL(new Blob([rows.map((r) => r.map(quote).join(",")).join("\n") + "\n"], { type: "text/csv" }));
    const a = document.createElement("a");
    a.href = url;
    a.download = "imported-access-keys.csv";
    a.click();
    URL.revokeObjectURL(url);
  };

  return (
    <div className="space-y-3">
      <div className="flex items-center gap-2 text-sm font-semibold text-ink">
        <KeyRound className="size-4 text-accent" />
        Users and policies: {count(result.keys.length)} access key{result.keys.length === 1 ? "" : "s"} and{" "}
        {count(result.groups.length)} group{result.groups.length === 1 ? "" : "s"} created
      </div>

      {result.keys.length > 0 && (
        <>
          <div className="flex flex-wrap items-center justify-between gap-2 rounded-md border border-warning bg-warning-soft p-3 text-sm text-warning">
            <span>These secret keys are shown only now. Save them before closing this window.</span>
            <Button icon={<Download className="size-4" />} onClick={downloadCSV}>
              Download CSV
            </Button>
          </div>
          <div className="max-h-60 overflow-auto rounded-lg border border-line">
            <table className="w-full text-xs">
              <thead className="bg-sunken/60 text-left">
                <tr className="label-strip">
                  <th className="px-3 py-2">Access key</th>
                  <th className="px-3 py-2">Secret key</th>
                  <th className="px-3 py-2">Access</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-line">
                {result.keys.map((k) => (
                  <tr key={k.accessKey} className="align-top">
                    <td className="px-3 py-2">
                      <div className="font-mono text-ink">{k.accessKey}</div>
                      <div className="text-muted">
                        {k.kind === "user" ? "User" : `Access key of ${k.parent}`}
                        {!k.enabled && " · disabled"}
                      </div>
                    </td>
                    <td className="px-3 py-2">
                      <button
                        type="button"
                        onClick={() => copy(k.secretKey, k.accessKey)}
                        className="flex items-center gap-1.5 font-mono text-ink hover:text-accent"
                        title="Copy secret key"
                      >
                        <span className="break-all">{k.secretKey}</span>
                        {copied === k.accessKey ? <Check className="size-3.5 shrink-0 text-ok" /> : <Copy className="size-3.5 shrink-0 text-muted" />}
                      </button>
                    </td>
                    <td className="px-3 py-2 text-muted">
                      {k.groups.length > 0 ? k.groups.join(", ") : k.note ? "" : "Own scope"}
                      {k.note && <div className="text-warning">{k.note}</div>}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </>
      )}

      {result.groups.length > 0 && (
        <div className="space-y-1 text-xs">
          {result.groups.map((g) => (
            <div key={g.name} className="flex flex-wrap gap-x-2">
              <CheckCircle2 className="size-3.5 shrink-0 text-ok" />
              <span className="font-mono text-ink">{g.name}</span>
              <span className="text-muted">
                from {g.from === "policy" ? "policy" : "group"} {g.source} · {g.readOnly ? "read-only" : "read-write"} ·{" "}
                {g.buckets.length > 0 ? g.buckets.join(", ") : "all buckets"}
              </span>
              {g.note && <span className="text-warning">{g.note}</span>}
            </div>
          ))}
        </div>
      )}

      {result.skipped.length > 0 && (
        <div className="space-y-1 rounded-md border border-line p-3 text-xs">
          <div className="font-medium text-ink">Not imported</div>
          {result.skipped.map((s) => (
            <div key={s.kind + s.name} className="flex gap-2">
              <CircleAlert className="size-3.5 shrink-0 text-warning" />
              <span>
                {kindLabels[s.kind]} <span className="font-mono text-ink">{s.name}</span>: <span className="text-muted">{s.reason}</span>
              </span>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
