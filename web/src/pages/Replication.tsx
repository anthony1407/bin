import { useState, useEffect } from "react";
import { AlertCircle, ArrowDownLeft, CheckCircle2, CircleAlert } from "lucide-react";
import { api, ApiError, type Bucket, type ImportJob, type ImportSkipped, type RemoteBucket } from "../api";
import { PageHeader, Button, Input, Modal, Spinner, cx, useToast } from "../components/ui";
import { ReplicationRules } from "./ReplicationRules";
import { bytes, count } from "../lib/format";

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

  const toast = useToast();

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


  // Import wizard functions
  function openImportModal() {
    setImportStep(1);
    setImportEndpoint("");
    setImportAccessKey("");
    setImportSecretKey("");
    setImportRegion("us-east-1");
    setRemoteBuckets([]);
    setSelectedRemoteBuckets(new Set());
    setImportError("");
    setImportErrors({});
    setImportJobs([]);
    setImportSkipped([]);
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
    if (!importSecretKey.trim()) {
      errs.importSecretKey = "Remote Secret Key is required.";
    }

    if (Object.keys(errs).length > 0) {
      setImportErrors(errs);
      setImportLoading(false);
      return;
    }

    try {
      const { buckets } = await api.importTest(importEndpoint, importAccessKey, importSecretKey, importRegion);
      setRemoteBuckets(buckets);
      // Buckets that already exist here would be skipped, so leave them unselected.
      const local = new Set(buckets.map((b) => b.name));
      setSelectedRemoteBuckets(new Set(buckets.filter((b) => !local.has(b.name)).map((b) => b.name)));
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

  async function importSelectedBuckets() {
    setImportLoading(true);
    setImportError("");
    setImportJobs([]);
    setImportSkipped([]);

    try {
      const { jobs, skipped } = await api.importStart(importEndpoint, importAccessKey, importSecretKey, importRegion, Array.from(selectedRemoteBuckets));
      api.buckets().then(setBuckets).catch(() => {});
      setImportJobs(jobs);
      setImportSkipped(skipped);
      setImportStep(3);
      toast("ok", `Import started for ${jobs.length} bucket(s).`);
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
          <Button
            variant="primary"
            busy={importLoading}
            disabled={selectedRemoteBuckets.size === 0}
            onClick={importSelectedBuckets}
          >
            Import Selected ({selectedRemoteBuckets.size})
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
          onClick={openImportModal}
          className="shrink-0"
        >
          Import from Remote
        </Button>
      </div>

      <ReplicationRules buckets={buckets} />


      {/* Import from Remote Wizard Modal */}
      <Modal
        open={importModal}
        onClose={closeImportModal}
        title="Import Buckets from Remote S3"
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
                <Input
                  id="import-secret-key"
                  type="password"
                  autoComplete="current-password"
                  value={importSecretKey}
                  onChange={(e) => {
                    setImportSecretKey(e.target.value);
                    clearImportError("importSecretKey");
                  }}
                  placeholder="••••••••••••••••"
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
            <p className="text-sm text-muted">
              Select the buckets to import. Each bucket is created here and its objects are copied
              once from the remote server; later changes there are not followed.
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
                    <span className="text-xs text-warning bg-warning-soft px-2 py-0.5 rounded">
                      Exists locally
                    </span>
                  )}
                </label>
              ))}
              {remoteBuckets.length === 0 && !importLoading && (
                <div className="p-8 text-center text-muted">No buckets found on remote server</div>
              )}
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
                <div className="text-xs text-muted">
                  {importJobs.filter((j) => j.state === "done").length} of {importJobs.length + importSkipped.length}{" "}
                  bucket(s) imported. You can close this window; the copy continues on the server.
                </div>
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
                        {job.state === "queued" ? "Waiting to start" : `${count(job.copied)} objects · ${bytes(job.bytes)}`}
                        {job.failed > 0 && <span className="text-danger"> · {count(job.failed)} failed</span>}
                        {job.state === "canceled" && " · canceled"}
                      </div>
                      {job.error && <div className="text-xs text-danger mt-0.5">{job.error}</div>}
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
          </div>
        )}
      </Modal>
    </div>
  );
}
