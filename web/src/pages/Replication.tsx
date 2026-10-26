import { useState, useEffect } from "react";
import { AlertCircle, ArrowDownLeft, Check, CheckCircle2, CircleAlert, Copy, Download, KeyRound } from "lucide-react";
import {
  api,
  ApiError,
  type Bucket,
  type ImportIAMResult,
  type ImportIAMSummary,
  type ImportJob,
  type ImportSkipped,
  type RemoteBucket,
} from "../api";
import { PageHeader, Button, Input, Modal, PasswordInput, Spinner, cx, useToast } from "../components/ui";
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
  // What else comes along: bucket settings, and the remote's identities (MinIO).
  const [remoteIam, setRemoteIam] = useState<ImportIAMSummary | null>(null);
  const [withSettings, setWithSettings] = useState(true);
  const [withIam, setWithIam] = useState(false);
  const [iamResult, setIamResult] = useState<ImportIAMResult | null>(null);
  const [iamError, setIamError] = useState("");

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
    if (!importSecretKey.trim()) {
      errs.importSecretKey = "Remote Secret Key is required.";
    }

    if (Object.keys(errs).length > 0) {
      setImportErrors(errs);
      setImportLoading(false);
      return;
    }

    try {
      const { buckets: remote, iam } = await api.importTest(importEndpoint, importAccessKey, importSecretKey, importRegion);
      setRemoteBuckets(remote);
      // Buckets that already exist here would be skipped, so leave them unselected.
      const local = new Set(buckets.map((b) => b.name));
      setSelectedRemoteBuckets(new Set(remote.filter((b) => !local.has(b.name)).map((b) => b.name)));
      setRemoteIam(iam);
      setWithIam(false);
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
    setIamResult(null);
    setIamError("");

    try {
      const res = await api.importStart(importEndpoint, importAccessKey, importSecretKey, importRegion, Array.from(selectedRemoteBuckets), {
        settings: withSettings,
        iam: withIam,
      });
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
            disabled={selectedRemoteBuckets.size === 0 && !withIam}
            onClick={importSelectedBuckets}
          >
            {selectedRemoteBuckets.size === 0 && withIam ? "Import Users & Policies" : `Import Selected (${selectedRemoteBuckets.size})`}
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
                <PasswordInput
                  id="import-secret-key"
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
                        {job.state === "queued" ? "Waiting to start" : `${count(job.copied)} objects · ${bytes(job.bytes)}`}
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
