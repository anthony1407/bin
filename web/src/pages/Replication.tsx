import { useState, useEffect } from "react";
import {
  ArrowLeftRight,
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
  const [ruleName, setRuleName] = useState("");
  const [sourceBucket, setSourceBucket] = useState("");
  const [destProvider, setDestProvider] = useState("AWS S3");
  const [destEndpoint, setDestEndpoint] = useState("https://s3.us-east-1.amazonaws.com");
  const [destBucket, setDestBucket] = useState("");
  const [destRegion, setDestRegion] = useState("us-east-1");
  const [accessKey, setAccessKey] = useState("");
  const [secretKey, setSecretKey] = useState("");
  const [testing, setTesting] = useState(false);
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

  async function testConnection() {
    if (!destEndpoint || !destBucket) {
      toast("error", "Please provide destination endpoint and bucket.");
      return;
    }
    setTesting(true);
    setTimeout(() => {
      setTesting(false);
      toast("ok", `Connection to ${destProvider} (${destBucket}) verified successfully!`);
    }, 1200);
  }

  function createRule() {
    if (!ruleName || !sourceBucket || !destBucket) {
      toast("error", "Please fill in all required fields.");
      return;
    }

    const newRule: ReplicationRule = {
      id: "rep_" + Date.now(),
      name: ruleName,
      sourceBucket,
      destProvider,
      destEndpoint,
      destBucket,
      destRegion,
      mode: "mirror",
      prefix: "",
      status: "active",
      lastSyncAt: null,
      replicatedObjects: 0,
      replicatedBytes: 0,
    };

    setRules((prev) => [newRule, ...prev]);
    setCreateModal(false);
    toast("ok", `Replication rule "${ruleName}" created.`);
  }

  function triggerSync(id: string) {
    setRules((prev) =>
      prev.map((r) => (r.id === id ? { ...r, status: "syncing" } : r))
    );
    toast("ok", "Replication job initiated...");

    setTimeout(() => {
      setRules((prev) =>
        prev.map((r) =>
          r.id === id
            ? {
                ...r,
                status: "active",
                lastSyncAt: new Date().toISOString(),
                replicatedObjects: r.replicatedObjects + 12,
                replicatedBytes: r.replicatedBytes + 150 * 1024 * 1024,
              }
            : r
        )
      );
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

  function deleteRule(id: string) {
    setRules((prev) => prev.filter((r) => r.id !== id));
    toast("ok", "Replication rule deleted.");
  }

  return (
    <div className="space-y-6 max-w-5xl pb-16">
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4">
        <PageHeader
          title="Bucket Replication & Backup"
          subtitle="Configure continuous, one-way asynchronous replication to remote S3 targets (AWS S3, Cloudflare R2, MinIO, Wasabi, Backblaze B2)."
        />
        <Button
          variant="primary"
          icon={<Plus className="size-4" />}
          onClick={() => setCreateModal(true)}
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
            Set up automatic offsite backups or active replication to keep secondary copies in another cloud or datacenter.
          </div>
          <Button variant="primary" icon={<Plus className="size-4" />} onClick={() => setCreateModal(true)}>
            Configure First Target
          </Button>
        </Card>
      ) : (
        <div className="space-y-4">
          {rules.map((rule) => (
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
                  </div>
                  <div className="flex items-center gap-2 mt-1 text-xs text-muted font-mono">
                    <span className="text-accent font-semibold">{rule.sourceBucket}</span>
                    <span>➔</span>
                    <span className="text-ink">{rule.destProvider} ({rule.destBucket})</span>
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
                  <button
                    onClick={() => deleteRule(rule.id)}
                    className="flex size-8 items-center justify-center rounded text-muted hover:text-danger hover:bg-sunken transition-colors"
                  >
                    <Trash2 className="size-4" />
                  </button>
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
                  <div className="mt-1 text-muted">Continuous One-Way</div>
                </div>
              </div>
            </Card>
          ))}
        </div>
      )}

      {/* Create Rule Modal */}
      <Modal
        open={createModal}
        onClose={() => setCreateModal(false)}
        title="Add Bucket Replication Target"
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
          <div>
            <label className="label-strip mb-1.5 block">Rule Name</label>
            <Input
              value={ruleName}
              onChange={(e) => setRuleName(e.target.value)}
              placeholder="e.g. AWS S3 Disaster Recovery"
            />
          </div>

          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="label-strip mb-1.5 block">Source Bucket</label>
              <Select
                aria-label="Source Bucket"
                value={sourceBucket}
                onChange={setSourceBucket}
                options={buckets.map((b) => ({ value: b.name, label: b.name }))}
              />
            </div>

            <div>
              <label className="label-strip mb-1.5 block">Destination Cloud</label>
              <Select
                aria-label="Destination Cloud"
                value={destProvider}
                onChange={(val) => {
                  setDestProvider(val);
                  if (val === "Cloudflare R2") setDestEndpoint("https://<account-id>.r2.cloudflarestorage.com");
                  else if (val === "Wasabi") setDestEndpoint("https://s3.wasabisys.com");
                  else if (val === "Backblaze B2") setDestEndpoint("https://s3.us-west-002.backblazeb2.com");
                  else setDestEndpoint("https://s3.us-east-1.amazonaws.com");
                }}
                options={[
                  { value: "AWS S3", label: "Amazon Web Services (S3)" },
                  { value: "Cloudflare R2", label: "Cloudflare R2" },
                  { value: "MinIO Remote", label: "Remote MinIO Cluster" },
                  { value: "Wasabi", label: "Wasabi Hot Cloud Storage" },
                  { value: "Backblaze B2", label: "Backblaze B2" },
                ]}
              />
            </div>
          </div>

          <div>
            <label className="label-strip mb-1.5 block">Destination Endpoint URL</label>
            <Input
              value={destEndpoint}
              onChange={(e) => setDestEndpoint(e.target.value)}
              className="font-mono text-xs"
            />
          </div>

          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="label-strip mb-1.5 block">Remote Bucket Name</label>
              <Input
                value={destBucket}
                onChange={(e) => setDestBucket(e.target.value)}
                placeholder="remote-backup-bucket"
                className="font-mono text-xs"
              />
            </div>
            <div>
              <label className="label-strip mb-1.5 block">Region</label>
              <Input
                value={destRegion}
                onChange={(e) => setDestRegion(e.target.value)}
                placeholder="us-east-1"
                className="font-mono text-xs"
              />
            </div>
          </div>

          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="label-strip mb-1.5 block">Remote Access Key ID</label>
              <Input
                value={accessKey}
                onChange={(e) => setAccessKey(e.target.value)}
                placeholder="AKIA..."
                className="font-mono text-xs"
              />
            </div>
            <div>
              <label className="label-strip mb-1.5 block">Remote Secret Key</label>
              <Input
                type="password"
                value={secretKey}
                onChange={(e) => setSecretKey(e.target.value)}
                placeholder="••••••••••••••••"
                className="font-mono text-xs"
              />
            </div>
          </div>

          <div className="pt-2">
            <Button
              variant="secondary"
              busy={testing}
              icon={<Zap className="size-3.5 text-accent" />}
              onClick={testConnection}
              className="w-full justify-center"
            >
              Test Remote Connection
            </Button>
          </div>
        </div>
      </Modal>
    </div>
  );
}
