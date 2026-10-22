import { useEffect, useState, type ReactNode } from "react";
import { Link, useNavigate, useParams } from "react-router";
import { Globe, Lock, Plus, Save, SlidersHorizontal, Trash2, X } from "lucide-react";
import { api, type Access, type Bucket, type Policy, type Rule } from "../api";
import { bytes, count, dateTime } from "../lib/format";
import { Badge, Button, Card, Field, IconButton, Input, Modal, Spinner, cx, errorText, useToast } from "../components/ui";
import { Select } from "../components/Select";
import { accessLabel } from "./Buckets";

const GiB = 1024 ** 3;

function Section({ title, description, children }: { title: string; description: ReactNode; children: ReactNode }) {
  return (
    <section className="grid gap-4 border-t border-line py-8 first:border-t-0 first:pt-0 lg:grid-cols-[16rem_1fr] lg:gap-10">
      <div>
        <h2 className="text-base font-semibold">{title}</h2>
        <p className="mt-1 text-sm text-muted">{description}</p>
      </div>
      <div className="min-w-0">{children}</div>
    </section>
  );
}

// --- access policy -------------------------------------------------------------------

const MODES: { mode: Policy["mode"]; title: string; body: string; icon: typeof Lock }[] = [
  { mode: "private", title: "Private", body: "Only signed requests from access keys.", icon: Lock },
  { mode: "public", title: "Public read", body: "Anyone can download objects by URL. Listing is not allowed.", icon: Globe },
  { mode: "custom", title: "Custom", body: "Anonymous access to chosen folders only.", icon: SlidersHorizontal },
];

const ACCESS_TEXT: Record<Access, string> = {
  readonly: "Read",
  writeonly: "Write",
  readwrite: "Read & write",
};

function PolicyEditor({ bucket, onSaved }: { bucket: Bucket; onSaved: (b: Bucket) => void }) {
  const [mode, setMode] = useState(bucket.policy.mode);
  const [rules, setRules] = useState<Rule[]>(bucket.policy.rules?.length ? bucket.policy.rules : [{ prefix: "", access: "readonly" }]);
  const [busy, setBusy] = useState(false);
  const toast = useToast();

  async function save() {
    const policy: Policy = mode === "custom" ? { mode, rules: rules.filter((r) => r.prefix.trim()) } : { mode };
    if (mode === "custom" && policy.rules!.length === 0) {
      toast("error", "Add at least one folder, or choose Private.");
      return;
    }
    setBusy(true);
    try {
      onSaved(await api.setPolicy(bucket.name, policy));
      toast("ok", "Access policy saved.");
    } catch (e) {
      toast("error", errorText(e));
    } finally {
      setBusy(false);
    }
  }

  const update = (i: number, patch: Partial<Rule>) => setRules((rs) => rs.map((r, j) => (j === i ? { ...r, ...patch } : r)));

  return (
    <div className="space-y-4">
      <div className="grid gap-2 sm:grid-cols-3" role="radiogroup" aria-label="Anonymous access">
        {MODES.map((m) => (
          <button
            key={m.mode}
            role="radio"
            aria-checked={mode === m.mode}
            onClick={() => setMode(m.mode)}
            className={cx(
              "rounded-md border p-3.5 text-left transition-colors",
              mode === m.mode ? "border-accent bg-accent-soft/60 ring-1 ring-accent" : "border-line-strong hover:bg-sunken",
            )}
          >
            <m.icon className={cx("mb-2 size-4", mode === m.mode ? "text-accent" : "text-faint")} />
            <div className="text-sm font-medium">{m.title}</div>
            <div className="mt-0.5 text-xs text-muted">{m.body}</div>
          </button>
        ))}
      </div>

      {mode === "public" && (
        <div className="rounded-md border border-warn/30 bg-warn-soft px-3.5 py-2.5 text-sm text-warn">
          Every object in <span className="font-mono">{bucket.name}</span> will be readable by anyone who knows its key.
        </div>
      )}

      {mode === "custom" && (
        <div className="rounded-md border border-line">
          <div className="label-strip grid grid-cols-[1fr_9rem_2.25rem] gap-2 border-b border-line bg-sunken/60 px-3 py-2">
            <span>folder prefix</span>
            <span>anonymous access</span>
            <span />
          </div>
          {rules.map((r, i) => (
            <div key={i} className="grid grid-cols-[1fr_9rem_2.25rem] items-center gap-2 border-b border-line px-3 py-2 last:border-b-0">
              <Input mono value={r.prefix} onChange={(e) => update(i, { prefix: e.target.value })} placeholder="shared/" aria-label="Folder prefix" />
              <Select
                aria-label="Anonymous access"
                value={r.access}
                onChange={(v) => update(i, { access: v as Access })}
                options={(Object.keys(ACCESS_TEXT) as Access[]).map((a) => ({ value: a, label: ACCESS_TEXT[a] }))}
              />
              <IconButton label="Remove rule" onClick={() => setRules((rs) => rs.filter((_, j) => j !== i))} disabled={rules.length === 1}>
                <X className="size-4" />
              </IconButton>
            </div>
          ))}
          <div className="flex items-center justify-between gap-3 bg-sunken/40 px-3 py-2">
            <span className="text-xs text-muted">Read lets anyone download and list; write lets anyone upload; read & write also allows deletes.</span>
            <Button variant="ghost" icon={<Plus className="size-4" />} onClick={() => setRules((rs) => [...rs, { prefix: "", access: "readonly" }])}>
              Add
            </Button>
          </div>
        </div>
      )}

      <div className="flex justify-end">
        <Button variant="primary" busy={busy} icon={<Save className="size-4" />} onClick={save}>
          Save access
        </Button>
      </div>
    </div>
  );
}

// --- quota ------------------------------------------------------------------------------

function QuotaEditor({ bucket, onSaved }: { bucket: Bucket; onSaved: (b: Bucket) => void }) {
  const [value, setValue] = useState(bucket.quotaBytes ? String(+(bucket.quotaBytes / GiB).toFixed(2)) : "");
  const [busy, setBusy] = useState(false);
  const toast = useToast();
  const gib = Number(value || 0);
  const invalid = !Number.isFinite(gib) || gib < 0;
  const use = bucket.quotaBytes ? Math.min(1, bucket.size / bucket.quotaBytes) : 0;

  async function save(bytesValue: number) {
    setBusy(true);
    try {
      const b = await api.setQuota(bucket.name, bytesValue);
      onSaved(b);
      if (!bytesValue) setValue("");
      toast("ok", bytesValue ? `Quota set to ${bytes(bytesValue)}.` : "Quota removed.");
    } catch (e) {
      toast("error", errorText(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="space-y-4">
      {bucket.quotaBytes > 0 && (
        <div>
          <div className="label-strip mb-1.5 flex justify-between">
            <span>used</span>
            <span>
              {bytes(bucket.size)} / {bytes(bucket.quotaBytes)} · {Math.round(use * 100)}%
            </span>
          </div>
          <div className="h-2 overflow-hidden rounded-full bg-sunken">
            <div className={cx("h-full", use > 0.9 ? "bg-danger" : use > 0.75 ? "bg-warn" : "bg-accent")} style={{ width: `${Math.max(1, use * 100)}%` }} />
          </div>
        </div>
      )}
      <div className="flex flex-wrap items-end gap-2">
        <div className="w-44">
          <Field label="Hard limit" error={invalid ? "Enter a positive number." : undefined}>
            <div className="relative">
              <Input value={value} onChange={(e) => setValue(e.target.value)} inputMode="decimal" placeholder="No limit" className="num pr-12" />
              <span className="pointer-events-none absolute top-1/2 right-3 -translate-y-1/2 font-mono text-xs text-muted">GiB</span>
            </div>
          </Field>
        </div>
        <Button variant="primary" busy={busy} disabled={invalid} onClick={() => save(Math.round(gib * GiB))}>
          Set quota
        </Button>
        {bucket.quotaBytes > 0 && (
          <Button variant="ghost" disabled={busy} onClick={() => save(0)}>
            Remove
          </Button>
        )}
      </div>
    </div>
  );
}

// --- tags ---------------------------------------------------------------------------------

function TagsEditor({ bucket, onSaved }: { bucket: Bucket; onSaved: (b: Bucket) => void }) {
  const [tags, setTags] = useState<[string, string][]>(Object.entries(bucket.tags ?? {}));
  const [key, setKey] = useState("");
  const [val, setVal] = useState("");
  const [busy, setBusy] = useState(false);
  const toast = useToast();

  async function save(next: [string, string][]) {
    setBusy(true);
    try {
      const b = await api.setTags(bucket.name, Object.fromEntries(next));
      setTags(Object.entries(b.tags ?? {}));
      onSaved(b);
    } catch (e) {
      toast("error", errorText(e));
    } finally {
      setBusy(false);
    }
  }

  function add() {
    const k = key.trim();
    if (!k) return;
    save([...tags.filter(([t]) => t !== k), [k, val.trim()]]).then(() => {
      setKey("");
      setVal("");
    });
  }

  return (
    <div className="space-y-3">
      {tags.length > 0 ? (
        <div className="flex flex-wrap gap-2">
          {tags.map(([k, v]) => (
            <span key={k} className="inline-flex items-center overflow-hidden rounded-md border border-line-strong font-mono text-xs">
              <span className="bg-sunken px-2 py-1 text-muted">{k}</span>
              <span className="px-2 py-1">{v || "—"}</span>
              <button
                onClick={() => save(tags.filter(([t]) => t !== k))}
                disabled={busy}
                aria-label={`Remove tag ${k}`}
                className="border-l border-line px-1.5 py-1 text-faint hover:bg-danger-soft hover:text-danger"
              >
                <X className="size-3" />
              </button>
            </span>
          ))}
        </div>
      ) : (
        <p className="text-sm text-faint">No tags.</p>
      )}
      <form
        className="flex flex-wrap gap-2"
        onSubmit={(e) => {
          e.preventDefault();
          add();
        }}
      >
        <Input mono value={key} onChange={(e) => setKey(e.target.value)} placeholder="key" aria-label="Tag key" className="w-36" />
        <Input mono value={val} onChange={(e) => setVal(e.target.value)} placeholder="value" aria-label="Tag value" className="w-44" />
        <Button type="submit" busy={busy} disabled={!key.trim()} icon={<Plus className="size-4" />}>
          Add tag
        </Button>
      </form>
    </div>
  );
}

// --- delete ----------------------------------------------------------------------------------

function DeleteBucket({ bucket }: { bucket: Bucket }) {
  const [open, setOpen] = useState(false);
  const [typed, setTyped] = useState("");
  const [busy, setBusy] = useState(false);
  const toast = useToast();
  const navigate = useNavigate();
  const empty = bucket.objects === 0;

  async function remove() {
    setBusy(true);
    try {
      await api.deleteBucket(bucket.name, !empty);
      toast("ok", `Bucket ${bucket.name} deleted.`);
      navigate("/buckets");
    } catch (e) {
      toast("error", errorText(e));
      setBusy(false);
    }
  }

  return (
    <>
      <Card className="flex flex-col gap-3 border-danger/30 p-4 sm:flex-row sm:items-center sm:justify-between">
        <p className="text-sm">
          {empty ? (
            "The bucket is empty."
          ) : (
            <>
              Deletes the bucket and <strong>all {count(bucket.objects)} objects</strong> ({bytes(bucket.size)}) in it.
            </>
          )}{" "}
          This cannot be undone.
        </p>
        <Button
          variant="danger"
          icon={<Trash2 className="size-4" />}
          onClick={() => {
            setTyped("");
            setOpen(true);
          }}
        >
          Delete bucket
        </Button>
      </Card>
      <Modal
        open={open}
        onClose={() => setOpen(false)}
        title="Delete bucket"
        footer={
          <>
            <Button onClick={() => setOpen(false)}>Cancel</Button>
            <Button variant="danger" busy={busy} disabled={typed !== bucket.name} icon={<Trash2 className="size-4" />} onClick={remove}>
              Delete forever
            </Button>
          </>
        }
      >
        <div className="space-y-4">
          <p className="text-sm">
            {empty ? "This permanently deletes the bucket." : `This permanently deletes ${count(bucket.objects)} objects and the bucket itself.`}
          </p>
          <Field label="Type the bucket name to confirm">
            <Input mono autoFocus value={typed} onChange={(e) => setTyped(e.target.value)} placeholder={bucket.name} />
          </Field>
        </div>
      </Modal>
    </>
  );
}

// --- page --------------------------------------------------------------------------------------

export function BucketSettingsPage() {
  const name = useParams().bucket!;
  const [bucket, setBucket] = useState<Bucket | null>(null);
  const toast = useToast();

  useEffect(() => {
    api.bucket(name).then(setBucket).catch((e) => toast("error", errorText(e)));
  }, [name, toast]);

  if (!bucket) {
    return (
      <div className="flex justify-center py-20">
        <Spinner />
      </div>
    );
  }
  const access = accessLabel(bucket);

  return (
    <div className="max-w-5xl">
      <div className="mb-8">
        <Link to={`/buckets/${encodeURIComponent(name)}`} className="label-strip hover:text-ink">
          ← {name}
        </Link>
        <h1 className="mt-1 text-2xl font-semibold tracking-tight">Bucket settings</h1>
        <div className="mt-5 grid grid-cols-2 overflow-hidden rounded-card border border-line bg-surface sm:grid-cols-4">
          {[
            ["objects", <span className="num">{count(bucket.objects)}</span>],
            ["size", <span className="num">{bytes(bucket.size)}</span>],
            [
              "access",
              <Badge tone={access.tone}>
                <access.icon className="size-3" />
                {access.text}
              </Badge>,
            ],
            ["created", <span className="text-sm">{dateTime(bucket.createdAt)}</span>],
          ].map(([label, value], i) => (
            <div key={i} className={cx("px-5 py-3.5", i > 0 && "border-l border-dashed border-line-strong", i === 2 && "max-sm:border-l-0 max-sm:border-t", i === 3 && "max-sm:border-t")}>
              <div className="label-strip">{label}</div>
              <div className="mt-1 text-lg text-ink">{value}</div>
            </div>
          ))}
        </div>
      </div>

      <Section
        title="Anonymous access"
        description="Who can reach objects without an access key. Signed requests are governed by each key's own scope."
      >
        <PolicyEditor key={JSON.stringify(bucket.policy)} bucket={bucket} onSaved={setBucket} />
      </Section>
      <Section title="Quota" description="Uploads that would push the bucket past this size are rejected with QuotaExceeded.">
        <QuotaEditor bucket={bucket} onSaved={setBucket} />
      </Section>
      <Section title="Tags" description="Free-form labels, also visible to S3 clients through GetBucketTagging.">
        <TagsEditor bucket={bucket} onSaved={setBucket} />
      </Section>
      <Section title="Delete bucket" description="Removes the bucket, its objects and any unfinished uploads.">
        <DeleteBucket bucket={bucket} />
      </Section>
    </div>
  );
}
