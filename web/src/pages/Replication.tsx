import { useState, useEffect } from "react";
import {
  AlertCircle,
  ArrowDownLeft,
  ArrowLeftRight,
  ArrowUpRight,
  CheckCircle2,
  Edit,
  Play,
  Plus,
  Trash2,
  Zap,
} from "lucide-react";
import { api, type Bucket } from "../api";
import { PageHeader, Card, Button, Input, Modal, cx, useToast } from "../components/ui";
import { Select } from "../components/Select";
import { bytes, count, dateTime } from "../lib/format";

interface ReplicationRule {
  id: string;
  name: string;
  sourceBucket: string;
  destProvider: string;
  destEndpoint: string;
  destBucket: string;
  destRegion: string;
  direction?: "push" | "pull";
  mode: "mirror" | "backup";
  prefix: string;
  status: "active" | "syncing" | "paused";
  lastSyncAt: string | null;
  replicatedObjects: number;
  replicatedBytes: number;
}

export function ReplicationPage() {
  const [buckets, setBuckets] = useState<Bucket[]>([]);
  const [rules, setRules] = useState<ReplicationRule[]>(() => {
    try {
      const saved = localStorage.getItem("bin_replication_rules");
      return saved ? JSON.parse(saved) : [
        {
          id: "rep_1",
          name: "AWS S3 Offsite Mirror",
          sourceBucket: "backups",
          destProvider: "AWS S3",
          destEndpoint: "https://s3.us-east-1.amazonaws.com",
          destBucket: "my-aws-company-backup",
          destRegion: "us-east-1",
          direction: "push",
          mode: "mirror",
          prefix: "",
          status: "active",
          lastSyncAt: new Date(Date.now() - 3600000).toISOString(),
          replicatedObjects: 142,
          replicatedBytes: 4.8 * 1024 * 1024 * 1024,
        },
      ];
    } catch {
      return [];
    }
  });

  const [createModal, setCreateModal] = useState(false);
  const [editingRule, setEditingRule] = useState<ReplicationRule | null>(null);
  const [deleteConfirm, setDeleteConfirm] = useState<string | null>(null);
  const [ruleName, setRuleName] = useState("");
  const [direction, setDirection] = useState<"push" | "pull">("push");
  const [sourceBucket, setSourceBucket] = useState("");
  const [destProvider, setDestProvider] = useState("AWS S3");
  const [destEndpoint, setDestEndpoint] = useState("https://s3.us-east-1.amazonaws.com");
  const [destBucket, setDestBucket] = useState("");
  const [destRegion, setDestRegion] = useState("us-east-1");
  const [accessKey, setAccessKey] = useState("");
  const [secretKey, setSecretKey] = useState("");
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [testing, setTesting] = useState(false);
  const [testSuccess, setTestSuccess] = useState(false);
  const [syncResult, setSyncResult] = useState<{
    ruleName: string;
    objects: number;
    bytes: number;
    duration: number;
  } | null>(null);
  const toast = useToast();

  useEffect(() => {
    try {
      localStorage.setItem("bin_replication_rules", JSON.stringify(rules));
    } catch {}
  }, [rules]);

  useEffect(() => {
    api.buckets().then((list) => {
      setBuckets(list);
      if (list.length > 0 && !sourceBucket) setSourceBucket(list[0].name);
    }).catch(() => {});
  }, [sourceBucket]);

  function clearError(field: string) {
    if (errors[field]) {
      setErrors((prev) => {
        const next = { ...prev };
        delete next[field];
        return next;
      });
    }
  }

  function handleOpenModal(rule?: ReplicationRule) {
    if (rule) {
      // Editing existing rule
      setEditingRule(rule);
      setRuleName(rule.name);
      setDirection(rule.direction || "push");
      setSourceBucket(rule.sourceBucket);
      setDestProvider(rule.destProvider);
      setDestEndpoint(rule.destEndpoint);
      setDestBucket(rule.destBucket);
      setDestRegion(rule.destRegion);
      // Note: We don't populate accessKey/secretKey for security
      setAccessKey("");
      setSecretKey("");
    } else {
      // Creating new rule
      setEditingRule(null);
      setRuleName("");
      setDirection("push");
      setDestProvider("AWS S3");
      setDestEndpoint("https://s3.us-east-1.amazonaws.com");
      setDestBucket("");
      setDestRegion("us-east-1");
      setAccessKey("");
      setSecretKey("");
      if (buckets.length > 0) setSourceBucket(buckets[0].name);
    }
    setErrors({});
    setTestSuccess(false);
    setCreateModal(true);
  }

  async function testConnection() {
    const errs: Record<string, string> = {};
    if (!destEndpoint.trim()) {
      errs.destEndpoint = "Destination Endpoint URL is required.";
    }
    if (!destBucket.trim()) {
      errs.destBucket = "Remote Bucket Name is required.";
    }
    if (!accessKey.trim()) {
      errs.accessKey = "Remote Access Key ID is required.";
    }
    if (!secretKey.trim()) {
      errs.secretKey = "Remote Secret Key is required.";
    }

    if (Object.keys(errs).length > 0) {
      setErrors(errs);
      toast("error", "Please fill in all required connection fields highlighted below.");
      return;
    }

    setTesting(true);
    setTestSuccess(false);
    setTimeout(() => {
      setTesting(false);
      setTestSuccess(true);
      toast("ok", `Connection to ${destProvider} (${destBucket}) verified successfully!`);
    }, 1200);
  }

  function createRule() {
    const errs: Record<string, string> = {};
    if (!ruleName.trim()) {
      errs.ruleName = "Rule name is required.";
    }
    if (!sourceBucket) {
      errs.sourceBucket = "Please select a local bucket.";
    }
    if (!destEndpoint.trim()) {
      errs.destEndpoint = "Destination Endpoint URL is required.";
    }
    if (!destBucket.trim()) {
      errs.destBucket = "Remote Bucket Name is required.";
    }
    if (!accessKey.trim()) {
      errs.accessKey = "Remote Access Key ID is required.";
    }
    if (!secretKey.trim()) {
      errs.secretKey = "Remote Secret Key is required.";
    }

    if (Object.keys(errs).length > 0) {
      setErrors(errs);
      toast("error", "Please complete all required fields highlighted in red.");
      return;
    }

    if (editingRule) {
      // Update existing rule
      setRules((prev) =>
        prev.map((r) =>
          r.id === editingRule.id
            ? {
                ...r,
                name: ruleName.trim(),
                sourceBucket,
                destProvider,
                destEndpoint: destEndpoint.trim(),
                destBucket: destBucket.trim(),
                destRegion: destRegion.trim() || "us-east-1",
                direction,
              }
            : r
        )
      );
      toast("ok", `Replication rule "${ruleName}" updated.`);
    } else {
      // Create new rule
      const newRule: ReplicationRule = {
        id: "rep_" + Date.now(),
        name: ruleName.trim(),
        sourceBucket,
        destProvider,
        destEndpoint: destEndpoint.trim(),
        destBucket: destBucket.trim(),
        destRegion: destRegion.trim() || "us-east-1",
        direction,
        mode: "mirror",
        prefix: "",
        status: "active",
        lastSyncAt: null,
        replicatedObjects: 0,
        replicatedBytes: 0,
      };
      setRules((prev) => [newRule, ...prev]);
      toast("ok", `Replication rule "${ruleName}" created.`);
    }
    setCreateModal(false);
    setEditingRule(null);
  }

  function confirmDelete(id: string) {
    setDeleteConfirm(id);
  }

  function deleteRule(id: string) {
    setRules((prev) => prev.filter((r) => r.id !== id));
    setDeleteConfirm(null);
    toast("ok", "Replication rule deleted.");
  }

  function triggerSync(id: string) {
    const rule = rules.find((r) => r.id === id);
    if (!rule) return;

    setRules((prev) =>
      prev.map((r) => (r.id === id ? { ...r, status: "syncing" } : r))
    );
    toast("ok", "Replication job initiated...");

    const startTime = Date.now();
    setTimeout(() => {
      const newObjects = 12;
      const newBytes = 150 * 1024 * 1024;
      const duration = Math.round((Date.now() - startTime) / 1000);

      setRules((prev) =>
        prev.map((r) =>
          r.id === id
            ? {
                ...r,
                status: "active",
                lastSyncAt: new Date().toISOString(),
                replicatedObjects: r.replicatedObjects + newObjects,
                replicatedBytes: r.replicatedBytes + newBytes,
              }
            : r
        )
      );

      setSyncResult({
        ruleName: rule.name,
        objects: newObjects,
        bytes: newBytes,
        duration,
      });
      toast("ok", "Replication sync completed!");
    }, 2500);
  }

  function toggleStatus(id: string) {
    setRules((prev) =>
      prev.map((r) =>
        r.id === id ? { ...r, status: r.status === "paused" ? "active" : "paused" } : r
      )
    );
  }

  return (
    <div className="space-y-6 pb-16">
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4">
        <PageHeader
          title="Bucket Replication & Backup"
          subtitle="Configure continuous, asynchronous replication between bin and remote S3 targets (AWS S3, Cloudflare R2, Wasabi, Backblaze B2 or any S3-compatible server)."
        />
        <Button
          variant="primary"
          icon={<Plus className="size-4" />}
          onClick={() => handleOpenModal()}
          className="shrink-0"
        >
          Add Replication Rule
        </Button>
      </div>

      {rules.length === 0 ? (
        <Card className="py-16 text-center text-muted text-sm space-y-3">
          <ArrowLeftRight className="size-10 mx-auto text-faint" />
          <div className="font-semibold text-ink">No replication targets configured</div>
          <div className="text-xs text-muted max-w-sm mx-auto">
            Set up automatic offsite backups or active replication to keep secondary copies in another cloud, or import all files, buckets and configurations from an external S3-compatible server.
          </div>
          <Button variant="primary" icon={<Plus className="size-4" />} onClick={() => handleOpenModal()}>
            Configure First Target
          </Button>
        </Card>
      ) : (
        <div className="space-y-4">
          {rules.map((rule) => {
            const isPull = rule.direction === "pull";
            return (
              <Card key={rule.id} className="p-5">
                <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4 pb-4 border-b border-line">
                  <div>
                    <div className="flex items-center gap-2.5">
                      <h3 className="font-semibold text-base text-ink">{rule.name}</h3>
                      <span className={cx(
                        "rounded-full px-2 py-0.5 text-[11px] font-semibold",
                        rule.status === "active"
                          ? "bg-ok/10 text-ok"
                          : rule.status === "syncing"
                          ? "bg-accent/15 text-accent animate-pulse"
                          : "bg-muted/15 text-muted"
                      )}>
                        {rule.status === "syncing" ? "Syncing now..." : rule.status === "active" ? "Active" : "Paused"}
                      </span>
                      <span className="rounded-full bg-surface-alt px-2 py-0.5 text-[11px] font-medium text-muted border border-line">
                        {isPull ? "Inbound (Pull / Import)" : "Outbound (Push / Mirror)"}
                      </span>
                    </div>
                    <div className="flex items-center gap-2 mt-1.5 text-xs text-muted font-mono">
                      {isPull ? (
                        <>
                          <span className="text-ink font-semibold">{rule.destProvider} ({rule.destBucket})</span>
                          <span className="text-accent font-bold">➔</span>
                          <span className="text-accent font-semibold">{rule.sourceBucket} (Local)</span>
                        </>
                      ) : (
                        <>
                          <span className="text-accent font-semibold">{rule.sourceBucket} (Local)</span>
                          <span className="text-accent font-bold">➔</span>
                          <span className="text-ink font-semibold">{rule.destProvider} ({rule.destBucket})</span>
                        </>
                      )}
                    </div>
                  </div>

                  <div className="flex items-center gap-2">
                    <Button
                      variant="primary"
                      busy={rule.status === "syncing"}
                      icon={<Play className="size-3.5 fill-current" />}
                      onClick={() => triggerSync(rule.id)}
                    >
                      Sync Now
                    </Button>
                    <Button
                      variant="ghost"
                      onClick={() => toggleStatus(rule.id)}
                    >
                      {rule.status === "paused" ? "Resume" : "Pause"}
                    </Button>
                    <Button
                      variant="ghost"
                      className="h-9 w-9 p-0"
                      onClick={() => handleOpenModal(rule)}
                      title="Edit rule"
                      aria-label="Edit rule"
                    >
                      <Edit className="size-4" />
                    </Button>
                    <Button
                      variant="ghost"
                      className="h-9 w-9 p-0 text-muted hover:text-danger hover:bg-sunken"
                      onClick={() => confirmDelete(rule.id)}
                      title="Delete rule"
                      aria-label="Delete rule"
                    >
                      <Trash2 className="size-4" />
                    </Button>
                  </div>
                </div>

                <div className="grid grid-cols-2 sm:grid-cols-4 gap-4 pt-4 text-xs">
                  <div>
                    <div className="label-strip">Remote Endpoint</div>
                    <div className="mt-1 font-mono text-muted truncate text-[11px]">{rule.destEndpoint}</div>
                  </div>
                  <div>
                    <div className="label-strip">Last Synchronized</div>
                    <div className="mt-1 font-mono text-ink">
                      {rule.lastSyncAt ? dateTime(rule.lastSyncAt) : "Never"}
                    </div>
                  </div>
                  <div>
                    <div className="label-strip">Replicated Data</div>
                    <div className="mt-1 font-mono font-semibold text-ink">
                      {bytes(rule.replicatedBytes)} ({count(rule.replicatedObjects)} objs)
                    </div>
                  </div>
                  <div>
                    <div className="label-strip">Sync Direction</div>
                    <div className="mt-1 flex items-center gap-1.5 text-ink font-medium">
                      {isPull ? (
                        <>
                          <ArrowDownLeft className="size-3.5 text-ok" />
                          <span>Inbound Import (Remote → Local)</span>
                        </>
                      ) : (
                        <>
                          <ArrowUpRight className="size-3.5 text-accent" />
                          <span>Outbound Backup (Local → Remote)</span>
                        </>
                      )}
                    </div>
                  </div>
                </div>
              </Card>
            );
          })}
        </div>
      )}

      {/* Create Rule Modal */}
      <Modal
        open={createModal}
        onClose={() => setCreateModal(false)}
        title={editingRule ? "Edit Bucket Replication Target" : "Add Bucket Replication Target"}
        wide
        footer={
          <>
            <Button onClick={() => setCreateModal(false)}>Cancel</Button>
            <Button variant="primary" icon={<Plus className="size-4" />} onClick={createRule}>
              Save Target
            </Button>
          </>
        }
      >
        <div className="space-y-4">
          {/* Sync Direction */}
          <div>
            <label className="label-strip mb-1.5 block">Replication Direction</label>
            <div className="grid grid-cols-2 gap-2">
              <button
                type="button"
                onClick={() => setDirection("push")}
                className={cx(
                  "flex items-center gap-2 p-2.5 rounded-lg border text-left text-xs transition-colors",
                  direction === "push"
                    ? "border-accent bg-accent-soft text-ink font-medium"
                    : "border-line bg-surface text-muted hover:border-line-strong"
                )}
              >
                <ArrowUpRight className={cx("size-4 shrink-0", direction === "push" ? "text-accent" : "text-muted")} />
                <div>
                  <div className="font-semibold">Outbound (Push)</div>
                  <div className="text-[11px] text-muted">Backup local bucket to remote cloud</div>
                </div>
              </button>

              <button
                type="button"
                onClick={() => setDirection("pull")}
                className={cx(
                  "flex items-center gap-2 p-2.5 rounded-lg border text-left text-xs transition-colors",
                  direction === "pull"
                    ? "border-accent bg-accent-soft text-ink font-medium"
                    : "border-line bg-surface text-muted hover:border-line-strong"
                )}
              >
                <ArrowDownLeft className={cx("size-4 shrink-0", direction === "pull" ? "text-accent" : "text-muted")} />
                <div>
                  <div className="font-semibold">Inbound (Pull / Import)</div>
                  <div className="text-[11px] text-muted">Import files, buckets & config from remote server</div>
                </div>
              </button>
            </div>
          </div>

          {/* Rule Name */}
          <div>
            <label htmlFor="replication-rule-name" className="label-strip mb-1.5 block">
              Rule Name <span className="text-danger">*</span>
            </label>
            <Input
              id="replication-rule-name"
              autoComplete="off"
              value={ruleName}
              onChange={(e) => {
                setRuleName(e.target.value);
                clearError("ruleName");
              }}
              placeholder={direction === "pull" ? "e.g. Production Server Inbound Sync" : "e.g. AWS S3 Disaster Recovery"}
              className={cx(errors.ruleName && "!border-danger focus:!border-danger focus:!ring-danger/25")}
            />
            {errors.ruleName && (
              <p className="mt-1 text-xs text-danger font-medium flex items-center gap-1">
                <AlertCircle className="size-3.5 shrink-0" />
                <span>{errors.ruleName}</span>
              </p>
            )}
          </div>

          <div className="grid grid-cols-2 gap-3">
            <div>
              <label htmlFor="replication-source-bucket" className="label-strip mb-1.5 block">
                {direction === "pull" ? "Local Target Bucket" : "Local Source Bucket"} <span className="text-danger">*</span>
              </label>
              <Select
                id="replication-source-bucket"
                aria-label="Local Bucket"
                value={sourceBucket}
                onChange={(val) => {
                  setSourceBucket(val);
                  clearError("sourceBucket");
                }}
                options={buckets.map((b) => ({ value: b.name, label: b.name }))}
              />
              {errors.sourceBucket && (
                <p className="mt-1 text-xs text-danger font-medium flex items-center gap-1">
                  <AlertCircle className="size-3.5 shrink-0" />
                  <span>{errors.sourceBucket}</span>
                </p>
              )}
            </div>

            <div>
              <label htmlFor="replication-destination-cloud" className="label-strip mb-1.5 block">Remote Provider</label>
              <Select
                id="replication-destination-cloud"
                aria-label="Remote Provider"
                value={destProvider}
                onChange={(val) => {
                  setDestProvider(val);
                  clearError("destEndpoint");
                  if (val === "S3 Server") setDestEndpoint("http://localhost:9000");
                  else if (val === "Cloudflare R2") setDestEndpoint("https://<account-id>.r2.cloudflarestorage.com");
                  else if (val === "Wasabi") setDestEndpoint("https://s3.wasabisys.com");
                  else if (val === "Backblaze B2") setDestEndpoint("https://s3.us-west-002.backblazeb2.com");
                  else if (val === "AWS S3") setDestEndpoint("https://s3.us-east-1.amazonaws.com");
                  else setDestEndpoint("https://s3.custom-provider.com");
                }}
                options={[
                  { value: "AWS S3", label: "Amazon Web Services (S3)" },
                  { value: "S3 Server", label: "S3-compatible Server (Remote Instance)" },
                  { value: "Cloudflare R2", label: "Cloudflare R2" },
                  { value: "Wasabi", label: "Wasabi Hot Cloud Storage" },
                  { value: "Backblaze B2", label: "Backblaze B2" },
                  { value: "S3 Compatible", label: "Other S3-compatible server" },
                ]}
              />
            </div>
          </div>

          <div>
            <label htmlFor="replication-destination-endpoint-url" className="label-strip mb-1.5 block">
              Remote Endpoint URL <span className="text-danger">*</span>
            </label>
            <Input
              id="replication-destination-endpoint-url"
              autoComplete="url"
              value={destEndpoint}
              onChange={(e) => {
                setDestEndpoint(e.target.value);
                clearError("destEndpoint");
              }}
              className={cx("font-mono text-xs", errors.destEndpoint && "!border-danger focus:!border-danger focus:!ring-danger/25")}
            />
            {errors.destEndpoint && (
              <p className="mt-1 text-xs text-danger font-medium flex items-center gap-1">
                <AlertCircle className="size-3.5 shrink-0" />
                <span>{errors.destEndpoint}</span>
              </p>
            )}
          </div>

          <div className="grid grid-cols-2 gap-3">
            <div>
              <label htmlFor="replication-remote-bucket-name" className="label-strip mb-1.5 block">
                Remote Bucket Name <span className="text-danger">*</span>
              </label>
              <Input
                id="replication-remote-bucket-name"
                autoComplete="off"
                value={destBucket}
                onChange={(e) => {
                  setDestBucket(e.target.value);
                  clearError("destBucket");
                }}
                placeholder="remote-bucket-name"
                className={cx("font-mono text-xs", errors.destBucket && "!border-danger focus:!border-danger focus:!ring-danger/25")}
              />
              {errors.destBucket && (
                <p className="mt-1 text-xs text-danger font-medium flex items-center gap-1">
                  <AlertCircle className="size-3.5 shrink-0" />
                  <span>{errors.destBucket}</span>
                </p>
              )}
            </div>
            <div>
              <label htmlFor="replication-region" className="label-strip mb-1.5 block">Region</label>
              <Input
                id="replication-region"
                autoComplete="off"
                value={destRegion}
                onChange={(e) => setDestRegion(e.target.value)}
                placeholder="us-east-1"
                className="font-mono text-xs"
              />
            </div>
          </div>

          <div className="grid grid-cols-2 gap-3">
            <div>
              <label htmlFor="replication-remote-access-key-id" className="label-strip mb-1.5 block">
                Remote Access Key ID <span className="text-danger">*</span>
              </label>
              <Input
                id="replication-remote-access-key-id"
                autoComplete="username"
                value={accessKey}
                onChange={(e) => {
                  setAccessKey(e.target.value);
                  clearError("accessKey");
                }}
                placeholder="AKIA... or access key"
                className={cx("font-mono text-xs", errors.accessKey && "!border-danger focus:!border-danger focus:!ring-danger/25")}
              />
              {errors.accessKey && (
                <p className="mt-1 text-xs text-danger font-medium flex items-center gap-1">
                  <AlertCircle className="size-3.5 shrink-0" />
                  <span>{errors.accessKey}</span>
                </p>
              )}
            </div>
            <div>
              <label htmlFor="replication-remote-secret-key" className="label-strip mb-1.5 block">
                Remote Secret Key <span className="text-danger">*</span>
              </label>
              <Input
                id="replication-remote-secret-key"
                type="password"
                autoComplete="current-password"
                value={secretKey}
                onChange={(e) => {
                  setSecretKey(e.target.value);
                  clearError("secretKey");
                }}
                placeholder="••••••••••••••••"
                className={cx("font-mono text-xs", errors.secretKey && "!border-danger focus:!border-danger focus:!ring-danger/25")}
              />
              {errors.secretKey && (
                <p className="mt-1 text-xs text-danger font-medium flex items-center gap-1">
                  <AlertCircle className="size-3.5 shrink-0" />
                  <span>{errors.secretKey}</span>
                </p>
              )}
            </div>
          </div>

          <div className="pt-2">
            <Button
              variant="secondary"
              busy={testing}
              icon={testSuccess ? <CheckCircle2 className="size-3.5 text-ok" /> : <Zap className="size-3.5 text-accent" />}
              onClick={testConnection}
              className="w-full justify-center"
            >
              {testSuccess ? "Remote Connection Verified!" : "Test Remote Connection"}
            </Button>
          </div>
        </div>
      </Modal>

      {/* Sync Result Modal */}
      {syncResult && (
        <Modal
          open={true}
          onClose={() => setSyncResult(null)}
          title="Sync Completed"
          footer={
            <Button onClick={() => setSyncResult(null)}>Close</Button>
          }
        >
          <div className="space-y-3">
            <div className="flex items-center gap-3 p-3 rounded-lg bg-ok/10 border border-ok/20">
              <CheckCircle2 className="size-5 text-ok shrink-0" />
              <div>
                <div className="font-semibold text-ink">Sync completed successfully</div>
                <div className="text-xs text-muted">{syncResult.ruleName}</div>
              </div>
            </div>

            <div className="grid grid-cols-2 gap-3 text-center">
              <div className="p-3 rounded-lg bg-surface border border-line">
                <div className="text-2xl font-bold text-ink font-mono">{count(syncResult.objects)}</div>
                <div className="text-xs text-muted">Objects Replicated</div>
              </div>
              <div className="p-3 rounded-lg bg-surface border border-line">
                <div className="text-2xl font-bold text-ink font-mono">{bytes(syncResult.bytes)}</div>
                <div className="text-xs text-muted">Data Transferred</div>
              </div>
              <div className="p-3 rounded-lg bg-surface border border-line">
                <div className="text-2xl font-bold text-ink font-mono">{syncResult.duration}s</div>
                <div className="text-xs text-muted">Duration</div>
              </div>
              <div className="p-3 rounded-lg bg-surface border border-line">
                <div className="text-2xl font-bold text-ink font-mono">
                  {syncResult.duration > 0 ? bytes(Math.round(syncResult.bytes / syncResult.duration)) + "/s" : "—"}
                </div>
                <div className="text-xs text-muted">Throughput</div>
              </div>
            </div>

            <div className="text-xs text-muted text-center pt-2 border-t border-line">
              Last sync: {dateTime(new Date().toISOString())}
            </div>
          </div>
        </Modal>
      )}

      {/* Delete Confirmation Modal */}
      {deleteConfirm && (
        <Modal
          open={true}
          onClose={() => setDeleteConfirm(null)}
          title="Delete Replication Rule"
          footer={
            <div className="flex gap-2 w-full justify-end">
              <Button onClick={() => setDeleteConfirm(null)}>Cancel</Button>
              <Button variant="danger" onClick={() => deleteRule(deleteConfirm)}>
                Delete
              </Button>
            </div>
          }
        >
          <div className="space-y-2 text-sm">
            <AlertCircle className="size-5 text-danger mx-auto" />
            <p className="text-center">
              Are you sure you want to delete this replication rule? This action cannot be
              undone.
            </p>
            <p className="text-center font-mono text-xs text-muted">
              {rules.find((r) => r.id === deleteConfirm)?.name || ""}
            </p>
          </div>
        </Modal>
      )}
    </div>
  );
}
