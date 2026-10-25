import { useCallback, useEffect, useState } from "react";
import {
  ArrowDownLeft,
  ArrowLeftRight,
  ArrowUpRight,
  CheckCircle2,
  CircleAlert,
  Edit,
  Pause,
  Play,
  Plus,
  RefreshCw,
  Trash2,
} from "lucide-react";
import { api, ApiError, type Bucket, type ReplicationRule, type ReplicationRuleInput } from "../api";
import { Badge, Button, Card, Field, Input, Modal, Spinner, cx, errorText, useToast } from "../components/ui";
import { Select } from "../components/Select";
import { bytes, count, dateTime, relative } from "../lib/format";

const INTERVALS = [
  { value: "60", label: "Every minute" },
  { value: "300", label: "Every 5 minutes" },
  { value: "900", label: "Every 15 minutes" },
  { value: "3600", label: "Every hour" },
  { value: "21600", label: "Every 6 hours" },
  { value: "86400", label: "Every day" },
];

const EMPTY: ReplicationRuleInput = {
  name: "",
  direction: "push",
  bucket: "",
  prefix: "",
  endpoint: "",
  accessKey: "",
  secretKey: "",
  region: "us-east-1",
  remoteBucket: "",
  remotePrefix: "",
  intervalSeconds: 300,
  mirrorDeletes: false,
  enabled: true,
};

// The rules poll while the page is open, so runs show up as they happen.
const POLL_MS = 3000;

function intervalLabel(s: number): string {
  return INTERVALS.find((i) => Number(i.value) === s)?.label.toLowerCase() ?? `every ${Math.round(s / 60)} min`;
}

/** Continuous replication rules: kept by the server, run on their schedule. */
export function ReplicationRules({ buckets }: { buckets: Bucket[] }) {
  const [rules, setRules] = useState<ReplicationRule[] | null>(null);
  const [editing, setEditing] = useState<ReplicationRule | "new" | null>(null);
  const [deleting, setDeleting] = useState<ReplicationRule | null>(null);
  const toast = useToast();

  const load = useCallback(() => api.replicationRules().then(setRules), []);

  useEffect(() => {
    load().catch((e) => {
      setRules([]);
      toast("error", errorText(e));
    });
    const t = setInterval(() => {
      if (document.visibilityState === "visible") load().catch(() => {});
    }, POLL_MS);
    return () => clearInterval(t);
  }, [load, toast]);

  async function syncNow(r: ReplicationRule) {
    try {
      await api.runReplicationRule(r.id);
      toast("ok", `Sync started for "${r.name}".`);
      load();
    } catch (e) {
      toast("error", errorText(e));
    }
  }

  async function setEnabled(r: ReplicationRule, enabled: boolean) {
    try {
      const { id: _, secretKeySet: __, createdAt: ___, running: ____, ...input } = r;
      await api.updateReplicationRule(r.id, { ...input, secretKey: "", enabled });
      toast("ok", enabled ? `"${r.name}" resumed.` : `"${r.name}" paused.`);
      load();
    } catch (e) {
      toast("error", errorText(e));
    }
  }

  async function remove(r: ReplicationRule) {
    try {
      await api.deleteReplicationRule(r.id);
      toast("ok", `Rule "${r.name}" deleted. Objects already copied stay where they are.`);
      setDeleting(null);
      load();
    } catch (e) {
      toast("error", errorText(e));
    }
  }

  return (
    <section className="space-y-4">
      <div className="flex flex-col gap-3 sm:flex-row sm:items-end sm:justify-between">
        <div>
          <h2 className="text-base font-semibold text-ink">Continuous replication</h2>
          <p className="text-xs text-muted">
            Keep a bucket folder in step with another S3 server, on a schedule. Only new or changed objects are copied.
          </p>
        </div>
        <Button variant="primary" icon={<Plus className="size-4" />} onClick={() => setEditing("new")} className="shrink-0">
          Add Replication Rule
        </Button>
      </div>

      {rules === null ? (
        <div className="flex justify-center py-12">
          <Spinner />
        </div>
      ) : rules.length === 0 ? (
        <Card className="space-y-3 py-14 text-center text-sm text-muted">
          <ArrowLeftRight className="mx-auto size-10 text-faint" />
          <div className="font-semibold text-ink">No replication rules yet</div>
          <div className="mx-auto max-w-md text-xs">
            Push a bucket to another cloud as an offsite backup, or pull a remote bucket into bin to keep a local copy.
          </div>
          <Button variant="primary" icon={<Plus className="size-4" />} onClick={() => setEditing("new")}>
            Add the first rule
          </Button>
        </Card>
      ) : (
        <div className="space-y-4">
          {rules.map((r) => (
            <RuleCard
              key={r.id}
              rule={r}
              onSync={() => syncNow(r)}
              onToggle={() => setEnabled(r, !r.enabled)}
              onEdit={() => setEditing(r)}
              onDelete={() => setDeleting(r)}
            />
          ))}
        </div>
      )}

      <RuleForm
        open={editing !== null}
        rule={editing === "new" ? null : editing}
        buckets={buckets}
        onClose={() => setEditing(null)}
        onSaved={() => {
          setEditing(null);
          load();
        }}
      />

      <Modal
        open={!!deleting}
        onClose={() => setDeleting(null)}
        title="Delete replication rule"
        footer={
          <>
            <Button onClick={() => setDeleting(null)}>Cancel</Button>
            <Button variant="danger" icon={<Trash2 className="size-4" />} onClick={() => deleting && remove(deleting)}>
              Delete
            </Button>
          </>
        }
      >
        <p className="text-sm text-muted">
          <span className="font-medium text-ink">{deleting?.name}</span> stops running. Objects it already copied stay on
          both sides.
        </p>
      </Modal>
    </section>
  );
}

function RuleCard({
  rule: r,
  onSync,
  onToggle,
  onEdit,
  onDelete,
}: {
  rule: ReplicationRule;
  onSync: () => void;
  onToggle: () => void;
  onEdit: () => void;
  onDelete: () => void;
}) {
  const push = r.direction === "push";
  const local = `bin: ${r.bucket}/${r.prefix}`;
  const remote = `${r.endpoint.replace(/^https?:\/\//, "")}: ${r.remoteBucket}/${r.remotePrefix}`;
  return (
    <Card className="p-5">
      <div className="flex flex-col justify-between gap-4 border-b border-line pb-4 sm:flex-row sm:items-center">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-base font-semibold text-ink">{r.name}</span>
            {r.running ? (
              <Badge tone="accent">syncing</Badge>
            ) : !r.enabled ? (
              <Badge>paused</Badge>
            ) : r.lastStatus === "error" ? (
              <Badge tone="danger">last run failed</Badge>
            ) : (
              <Badge tone="ok">active</Badge>
            )}
            {r.mirrorDeletes && <Badge tone="warn">mirrors deletes</Badge>}
          </div>
          <div className="mt-1.5 flex flex-wrap items-center gap-2 font-mono text-xs text-muted">
            <span className="truncate">{push ? local : remote}</span>
            {push ? <ArrowUpRight className="size-3.5 shrink-0 text-accent" /> : <ArrowDownLeft className="size-3.5 shrink-0 text-ok" />}
            <span className="truncate">{push ? remote : local}</span>
          </div>
        </div>
        <div className="flex shrink-0 items-center gap-2">
          <Button
            icon={<RefreshCw className={cx("size-4", r.running && "animate-spin")} />}
            onClick={onSync}
            disabled={r.running}
          >
            {r.running ? "Syncing…" : "Sync now"}
          </Button>
          <Button icon={r.enabled ? <Pause className="size-4" /> : <Play className="size-4" />} onClick={onToggle}>
            {r.enabled ? "Pause" : "Resume"}
          </Button>
          <Button variant="ghost" onClick={onEdit} title="Edit rule" aria-label="Edit rule" icon={<Edit className="size-4" />} />
          <Button variant="ghost" onClick={onDelete} title="Delete rule" aria-label="Delete rule" className="text-danger" icon={<Trash2 className="size-4" />} />
        </div>
      </div>

      <div className="grid grid-cols-2 gap-4 pt-4 text-xs sm:grid-cols-4">
        <div>
          <div className="label-strip">Schedule</div>
          <div className="mt-1 text-ink">{intervalLabel(r.intervalSeconds)}</div>
          {r.enabled && r.nextRunAt && !r.running && <div className="text-muted">next {relative(r.nextRunAt)}</div>}
        </div>
        <div>
          <div className="label-strip">Last run</div>
          <div className="mt-1 flex items-center gap-1.5 text-ink">
            {r.lastRunAt ? (
              <>
                {r.lastStatus === "error" ? <CircleAlert className="size-3.5 text-danger" /> : <CheckCircle2 className="size-3.5 text-ok" />}
                <span title={dateTime(r.lastRunAt)}>{relative(r.lastRunAt)}</span>
              </>
            ) : (
              "Never"
            )}
          </div>
          {r.lastRunAt && (
            <div className="text-muted">
              {count(r.lastCopied)} copied · {count(r.lastDeleted)} deleted{r.lastFailed > 0 && ` · ${count(r.lastFailed)} failed`}
            </div>
          )}
        </div>
        <div>
          <div className="label-strip">Last transfer</div>
          <div className="mt-1 font-mono text-ink">{bytes(r.lastBytes)}</div>
        </div>
        <div>
          <div className="label-strip">Since created</div>
          <div className="mt-1 font-mono text-ink">
            {count(r.totalCopied)} objects · {bytes(r.totalBytes)}
          </div>
        </div>
      </div>
      {r.lastStatus === "error" && r.lastError && (
        <div className="mt-3 rounded-md border border-danger/30 bg-danger-soft px-3 py-2 text-xs text-danger">{r.lastError}</div>
      )}
    </Card>
  );
}

type Errors = Record<string, string>;

function RuleForm({
  open,
  rule,
  buckets,
  onClose,
  onSaved,
}: {
  open: boolean;
  rule: ReplicationRule | null;
  buckets: Bucket[];
  onClose: () => void;
  onSaved: () => void;
}) {
  const [form, setForm] = useState<ReplicationRuleInput>(EMPTY);
  const [errors, setErrors] = useState<Errors>({});
  const [saving, setSaving] = useState(false);
  const [testing, setTesting] = useState(false);
  const [test, setTest] = useState<{ ok: boolean; text: string } | null>(null);
  const toast = useToast();

  useEffect(() => {
    if (!open) return;
    setErrors({});
    setTest(null);
    if (rule) {
      const { id: _, secretKeySet: __, createdAt: ___, running: ____, ...input } = rule;
      setForm({ ...EMPTY, ...input, secretKey: "" });
    } else {
      setForm({ ...EMPTY, bucket: buckets[0]?.name ?? "" });
    }
  }, [open, rule, buckets]);

  const set = <K extends keyof ReplicationRuleInput>(k: K, v: ReplicationRuleInput[K]) => {
    setForm((f) => ({ ...f, [k]: v }));
    setErrors(({ [k]: _, "": __, ...rest }) => rest);
  };

  function validate(): Errors {
    const e: Errors = {};
    if (!form.name.trim()) e.name = "Name is required.";
    if (!form.bucket) e.bucket = "Local bucket is required.";
    if (!form.endpoint.trim()) e.endpoint = "Remote endpoint is required.";
    else if (!/^https?:\/\/[^/]+\/?$/.test(form.endpoint.trim())) e.endpoint = "Use an http:// or https:// URL with no path.";
    if (!form.accessKey.trim()) e.accessKey = "Remote access key is required.";
    if (!form.secretKey && !rule?.secretKeySet) e.secretKey = "Remote secret key is required.";
    if (!form.remoteBucket.trim()) e.remoteBucket = "Remote bucket is required.";
    return e;
  }

  async function testConnection() {
    setTest(null);
    const e = validate();
    const connection = ["endpoint", "accessKey", "secretKey"].filter((k) => e[k]);
    if (connection.length) {
      setErrors(Object.fromEntries(connection.map((k) => [k, e[k]])));
      return;
    }
    if (!form.secretKey) {
      setErrors({ secretKey: "Enter the secret again to test the connection." });
      return;
    }
    setTesting(true);
    try {
      const { buckets: list } = await api.importTest(form.endpoint, form.accessKey, form.secretKey, form.region);
      const found = list.some((b) => b.name === form.remoteBucket.trim());
      setTest(
        !form.remoteBucket.trim() || found
          ? { ok: true, text: `Connected. The remote has ${list.length} bucket(s)${found ? `, including ${form.remoteBucket}` : ""}.` }
          : { ok: false, text: `Connected, but there is no bucket "${form.remoteBucket}" there. Create it first.` },
      );
    } catch (err) {
      setTest({ ok: false, text: errorText(err) });
    } finally {
      setTesting(false);
    }
  }

  async function save() {
    const e = validate();
    setErrors(e);
    if (Object.keys(e).length) return;
    setSaving(true);
    try {
      if (rule) await api.updateReplicationRule(rule.id, form);
      else await api.createReplicationRule(form);
      toast("ok", rule ? "Rule updated." : form.enabled ? "Rule created; the first sync has started." : "Rule created.");
      onSaved();
    } catch (err) {
      if (err instanceof ApiError && typeof err.data.field === "string" && err.data.field) {
        setErrors({ [err.data.field]: err.message });
      } else {
        setErrors({ "": errorText(err) });
      }
    } finally {
      setSaving(false);
    }
  }

  const push = form.direction === "push";

  return (
    <Modal
      open={open}
      onClose={onClose}
      size="lg"
      title={rule ? `Edit replication rule` : "Add replication rule"}
      footer={
        <>
          <Button onClick={testConnection} busy={testing}>
            Test connection
          </Button>
          <Button onClick={onClose}>Cancel</Button>
          <Button variant="primary" busy={saving} onClick={save}>
            {rule ? "Save" : "Create rule"}
          </Button>
        </>
      }
    >
      <div className="space-y-5">
        <div>
          <span className="mb-1.5 block text-sm font-medium text-ink">Direction</span>
          <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
            {(["push", "pull"] as const).map((d) => (
              <button
                key={d}
                type="button"
                onClick={() => set("direction", d)}
                className={cx(
                  "flex items-start gap-2.5 rounded-md border p-3 text-left",
                  form.direction === d ? "border-accent bg-accent-soft" : "border-line-strong hover:bg-sunken",
                )}
              >
                {d === "push" ? <ArrowUpRight className="mt-0.5 size-4 text-accent" /> : <ArrowDownLeft className="mt-0.5 size-4 text-ok" />}
                <span>
                  <span className="block text-sm font-medium text-ink">{d === "push" ? "Push: bin → remote" : "Pull: remote → bin"}</span>
                  <span className="block text-xs text-muted">
                    {d === "push" ? "Offsite backup of a bin bucket." : "Keep a local copy of a remote bucket."}
                  </span>
                </span>
              </button>
            ))}
          </div>
        </div>

        <Field label="Rule name" required error={errors.name}>
          <Input value={form.name} aria-invalid={!!errors.name} onChange={(e) => set("name", e.target.value)} placeholder="e.g. Offsite backup to R2" />
        </Field>

        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <div>
            <label htmlFor="repl-bucket" className="mb-1.5 block text-sm font-medium text-ink">
              Local bucket <span className="text-danger">*</span>
            </label>
            <Select
              id="repl-bucket"
              aria-label="Local bucket"
              value={form.bucket}
              onChange={(v) => set("bucket", v)}
              options={buckets.map((b) => ({ value: b.name, label: b.name }))}
              emptyText="No buckets yet"
            />
            {errors.bucket && <span className="mt-1.5 block text-xs font-medium text-danger">{errors.bucket}</span>}
          </div>
          <Field label="Local folder" hint={push ? "Only this folder is pushed. Empty: the whole bucket." : "Pulled objects land here. Empty: the bucket root."}>
            <Input mono value={form.prefix} onChange={(e) => set("prefix", e.target.value)} placeholder="e.g. photos/2026/" />
          </Field>
        </div>

        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <div className="sm:col-span-2">
            <Field label="Remote endpoint" required hint="AWS: https://s3.<region>.amazonaws.com · R2: https://<account>.r2.cloudflarestorage.com" error={errors.endpoint}>
              <Input mono value={form.endpoint} aria-invalid={!!errors.endpoint} onChange={(e) => set("endpoint", e.target.value)} placeholder="https://s3.us-east-1.amazonaws.com" />
            </Field>
          </div>
          <Field label="Access key" required error={errors.accessKey}>
            <Input mono value={form.accessKey} aria-invalid={!!errors.accessKey} onChange={(e) => set("accessKey", e.target.value)} autoComplete="off" />
          </Field>
          <Field label="Secret key" required={!rule?.secretKeySet} hint={rule?.secretKeySet ? "Stored. Leave blank to keep it." : undefined} error={errors.secretKey}>
            <Input
              type="password"
              autoComplete="new-password"
              value={form.secretKey ?? ""}
              aria-invalid={!!errors.secretKey}
              onChange={(e) => set("secretKey", e.target.value)}
              placeholder={rule?.secretKeySet ? "••••••••" : ""}
            />
          </Field>
          <Field label="Region">
            <Input mono value={form.region} onChange={(e) => set("region", e.target.value)} placeholder="us-east-1" />
          </Field>
          <Field label="Remote bucket" required hint="Must already exist." error={errors.remoteBucket}>
            <Input mono value={form.remoteBucket} aria-invalid={!!errors.remoteBucket} onChange={(e) => set("remoteBucket", e.target.value)} placeholder="my-backup-bucket" />
          </Field>
          <div className="sm:col-span-2">
            <Field label="Remote folder" hint={push ? "Pushed objects land here. Empty: the bucket root." : "Only this folder is pulled. Empty: the whole bucket."}>
              <Input mono value={form.remotePrefix} onChange={(e) => set("remotePrefix", e.target.value)} placeholder="e.g. bin-backup/" />
            </Field>
          </div>
        </div>

        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <div>
            <label htmlFor="repl-interval" className="mb-1.5 block text-sm font-medium text-ink">
              Schedule
            </label>
            <Select
              id="repl-interval"
              aria-label="Schedule"
              value={String(form.intervalSeconds)}
              onChange={(v) => set("intervalSeconds", Number(v))}
              options={INTERVALS.some((i) => Number(i.value) === form.intervalSeconds) ? INTERVALS : [...INTERVALS, { value: String(form.intervalSeconds), label: intervalLabel(form.intervalSeconds) }]}
            />
            {errors.intervalSeconds && <span className="mt-1.5 block text-xs font-medium text-danger">{errors.intervalSeconds}</span>}
          </div>
          <div className="space-y-2 pt-1">
            <label className="flex items-start gap-2 text-sm">
              <input type="checkbox" className="mt-0.5 size-4 accent-[var(--accent)]" checked={form.mirrorDeletes} onChange={(e) => set("mirrorDeletes", e.target.checked)} />
              <span>
                Mirror deletes
                <span className="block text-xs text-muted">
                  Remove from the {push ? "remote" : "local"} side what is deleted at the source. Only objects this rule copied are ever removed.
                </span>
              </span>
            </label>
            <label className="flex items-center gap-2 text-sm">
              <input type="checkbox" className="size-4 accent-[var(--accent)]" checked={form.enabled} onChange={(e) => set("enabled", e.target.checked)} />
              Run on schedule {!rule && "(and sync once right away)"}
            </label>
          </div>
        </div>

        {test && (
          <div className={cx("rounded-md border px-3 py-2 text-xs", test.ok ? "border-ok/30 bg-ok-soft text-ok" : "border-danger/30 bg-danger-soft text-danger")}>
            {test.text}
          </div>
        )}
        {errors[""] && <div className="rounded-md border border-danger/30 bg-danger-soft px-3 py-2 text-xs text-danger">{errors[""]}</div>}
      </div>
    </Modal>
  );
}
