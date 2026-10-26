import { useEffect, useState, type ReactNode } from "react";
import { Link, useNavigate, useParams } from "react-router";
import { AlertTriangle, Clock, Code2, Globe, Info, Lock, Plus, Save, Shield, SlidersHorizontal, Trash2, X } from "lucide-react";
import { api, type Access, type Bucket, type CORSRule, type Policy, type Rule } from "../api";
import { bytes, count, dateTime } from "../lib/format";
import { Badge, Button, Card, ComingSoon, Field, IconButton, Input, Modal, Spinner, cx, errorText, useToast } from "../components/ui";
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
  { mode: "public", title: "Public read", body: "Anyone can download objects by URL and list the bucket.", icon: Globe },
  { mode: "custom", title: "Custom", body: "Anonymous access to chosen folders and files only.", icon: SlidersHorizontal },
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
            <span>folder prefix or file</span>
            <span>anonymous access</span>
            <span />
          </div>
          {rules.map((r, i) => (
            <div key={i} className="grid grid-cols-[1fr_9rem_2.25rem] items-center gap-2 border-b border-line px-3 py-2 last:border-b-0">
              <div className="relative">
                <Input
                  mono
                  value={r.prefix}
                  onChange={(e) => update(i, { prefix: e.target.value })}
                  placeholder="shared/"
                  aria-label={r.exact ? "File key" : "Folder prefix"}
                  className={r.exact ? "pr-12" : undefined}
                />
                {r.exact && (
                  <span className="pointer-events-none absolute top-1/2 right-2.5 -translate-y-1/2 text-[10px] font-semibold text-faint uppercase" title="Only this exact file">
                    file
                  </span>
                )}
              </div>
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
  // A versioned bucket can hold delete markers, which are not counted as objects.
  const empty = bucket.objects === 0 && bucket.versioning === "";

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
              Deletes the bucket and <strong>all {count(bucket.objects)} {bucket.versioning ? "object versions" : "objects"}</strong> ({bytes(bucket.size)}) in it.
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
            {empty
              ? "This permanently deletes the bucket."
              : `This permanently deletes ${count(bucket.objects)} ${bucket.versioning ? "object versions, every delete marker" : "objects"} and the bucket itself.`}
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
    <div>
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

      <Section
        title="Versioning"
        description="Keeps every version of each object: overwrites and deletes can be undone from the explorer. Once turned on, versioning can be suspended but not turned off."
      >
        <VersioningPanel bucket={bucket} onSaved={setBucket} />
      </Section>

      <Section
        title="CORS"
        description={<>Which websites may call the S3 API for <span className="font-mono text-xs">{bucket.name}</span> from a browser, for example to upload with a presigned URL. Requests from other apps and tools are not affected.</>}
      >
        <CORSEditor key={JSON.stringify(bucket.cors)} bucket={bucket} onSaved={setBucket} />
      </Section>

      {/* Lifecycle Rules */}
      <Section
        title="Lifecycle Rules"
        description="Automatically manage objects over time. Set rules to expire (delete) objects after a specified number of days, or clean up incomplete multipart uploads."
      >
        <Preview note="Lifecycle rules are planned; objects are not expired automatically yet.">
          <LifecycleEditor bucket={bucket} />
        </Preview>
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

// --- previews ------------------------------------------------------------------------

/** A section that is not connected to the server yet: shown, but not usable. */
function Preview({ note, children }: { note: string; children: ReactNode }) {
  return (
    <div className="space-y-3">
      <ComingSoon>{note}</ComingSoon>
      <fieldset disabled aria-disabled="true" className="pointer-events-none select-none opacity-60">
        {children}
      </fieldset>
    </div>
  );
}

// --- versioning ----------------------------------------------------------------------

function VersioningPanel({ bucket, onSaved }: { bucket: Bucket; onSaved: (b: Bucket) => void }) {
  const [confirm, setConfirm] = useState<"Enabled" | "Suspended" | null>(null);
  const [busy, setBusy] = useState(false);
  const toast = useToast();
  const status = bucket.versioning;

  async function save(next: "Enabled" | "Suspended") {
    setBusy(true);
    try {
      onSaved(await api.setVersioning(bucket.name, next));
      toast("ok", next === "Enabled" ? "Versioning enabled." : "Versioning suspended.");
      setConfirm(null);
    } catch (e) {
      toast("error", errorText(e));
    } finally {
      setBusy(false);
    }
  }

  const state = {
    "": { icon: Info, tone: "bg-sunken text-faint", title: "Off", body: "Overwriting or deleting an object replaces it for good." },
    Enabled: { icon: Shield, tone: "bg-ok-soft text-ok", title: "Enabled", body: "Every overwrite and delete keeps the previous version. Restore them from the Versions tab of a file." },
    Suspended: { icon: AlertTriangle, tone: "bg-warn-soft text-warn", title: "Suspended", body: "New writes replace each other again; versions kept until now stay and can still be restored." },
  }[status];

  return (
    <div className="space-y-4">
      <div className="flex items-center gap-3">
        <div className={cx("flex size-10 shrink-0 items-center justify-center rounded-full", state.tone)}>
          <state.icon className="size-5" />
        </div>
        <div>
          <div className="text-sm font-semibold">{state.title}</div>
          <div className="text-xs text-muted">{state.body}</div>
        </div>
      </div>
      <div className="flex flex-wrap gap-2">
        {status !== "Enabled" && (
          <Button variant="primary" icon={<Shield className="size-4" />} onClick={() => setConfirm("Enabled")}>
            {status === "Suspended" ? "Resume versioning" : "Enable versioning"}
          </Button>
        )}
        {status === "Enabled" && <Button onClick={() => setConfirm("Suspended")}>Suspend versioning</Button>}
      </div>
      <Modal
        open={confirm !== null}
        onClose={() => !busy && setConfirm(null)}
        title={confirm === "Enabled" ? "Enable versioning" : "Suspend versioning"}
        footer={
          <>
            <Button disabled={busy} onClick={() => setConfirm(null)}>
              Cancel
            </Button>
            <Button variant="primary" busy={busy} onClick={() => confirm && save(confirm)}>
              {confirm === "Enabled" ? "Enable" : "Suspend"}
            </Button>
          </>
        }
      >
        {confirm === "Enabled" ? (
          <div className="space-y-2 text-sm">
            <p>From now on, every overwrite and delete in <span className="font-mono">{bucket.name}</span> keeps the previous version.</p>
            <p className="text-muted">
              Older versions count toward the bucket's size and quota until you delete them. Versioning can be suspended later, but a
              versioned bucket never becomes unversioned again.
            </p>
          </div>
        ) : (
          <div className="space-y-2 text-sm">
            <p>New writes will replace each other again, as in an unversioned bucket.</p>
            <p className="text-muted">The versions kept so far are not deleted; you can still restore or remove them one by one.</p>
          </div>
        )}
      </Modal>
    </div>
  );
}

// --- CORS --------------------------------------------------------------------------------

const HTTP_METHODS = ["GET", "PUT", "POST", "DELETE", "HEAD"];

/** A rule as edited: lists are typed as comma- or line-separated text. */
interface CORSDraft {
  key: number;
  origins: string;
  methods: string[];
  headers: string;
  expose: string;
  maxAge: string;
}

let corsDraftKey = 0;

const splitList = (s: string) =>
  s
    .split(/[\n,]/)
    .map((v) => v.trim())
    .filter(Boolean);

function toDraft(r: CORSRule): CORSDraft {
  return {
    key: ++corsDraftKey,
    origins: r.allowedOrigins.join(", "),
    methods: r.allowedMethods,
    headers: (r.allowedHeaders ?? []).join(", "),
    expose: (r.exposeHeaders ?? []).join(", "),
    maxAge: r.maxAgeSeconds === undefined ? "" : String(r.maxAgeSeconds),
  };
}

function fromDraft(d: CORSDraft): CORSRule {
  const rule: CORSRule = { allowedOrigins: splitList(d.origins), allowedMethods: d.methods };
  const headers = splitList(d.headers);
  const expose = splitList(d.expose);
  if (headers.length) rule.allowedHeaders = headers;
  if (expose.length) rule.exposeHeaders = expose;
  if (d.maxAge.trim() !== "") rule.maxAgeSeconds = Number(d.maxAge);
  return rule;
}

/** A typical rule for a web app that uploads with presigned URLs and reads ETags. */
const newDraft = (): CORSDraft => ({
  key: ++corsDraftKey,
  origins: "https://app.example.com",
  methods: ["GET", "PUT", "HEAD"],
  headers: "*",
  expose: "ETag",
  maxAge: "3600",
});

function CORSEditor({ bucket, onSaved }: { bucket: Bucket; onSaved: (b: Bucket) => void }) {
  const [drafts, setDrafts] = useState<CORSDraft[]>(() => bucket.cors.map(toDraft));
  const [busy, setBusy] = useState(false);
  const [showXML, setShowXML] = useState(false);
  const toast = useToast();
  const dirty = JSON.stringify(drafts.map(fromDraft)) !== JSON.stringify(bucket.cors);

  const update = (key: number, patch: Partial<CORSDraft>) => setDrafts((ds) => ds.map((d) => (d.key === key ? { ...d, ...patch } : d)));
  const toggleMethod = (d: CORSDraft, m: string) =>
    update(d.key, { methods: d.methods.includes(m) ? d.methods.filter((x) => x !== m) : HTTP_METHODS.filter((x) => x === m || d.methods.includes(x)) });

  function problem(d: CORSDraft): string | null {
    if (splitList(d.origins).length === 0) return "Add at least one origin.";
    if (splitList(d.origins).some((o) => o.split("*").length > 2)) return "An origin can contain at most one *.";
    if (d.methods.length === 0) return "Choose at least one method.";
    if (d.maxAge.trim() !== "" && !/^\d+$/.test(d.maxAge.trim())) return "Max age is a whole number of seconds.";
    return null;
  }
  const invalid = drafts.some((d) => problem(d) !== null);

  async function save() {
    setBusy(true);
    try {
      const b = await api.setCORS(bucket.name, drafts.map(fromDraft));
      onSaved(b);
      toast("ok", b.cors.length ? "CORS rules saved." : "CORS rules removed.");
    } catch (e) {
      toast("error", errorText(e));
    } finally {
      setBusy(false);
    }
  }

  const xml = () =>
    `<CORSConfiguration>\n${drafts
      .map(fromDraft)
      .map((r) =>
        [
          "  <CORSRule>",
          ...r.allowedOrigins.map((o) => `    <AllowedOrigin>${o}</AllowedOrigin>`),
          ...r.allowedMethods.map((m) => `    <AllowedMethod>${m}</AllowedMethod>`),
          ...(r.allowedHeaders ?? []).map((h) => `    <AllowedHeader>${h}</AllowedHeader>`),
          ...(r.exposeHeaders ?? []).map((h) => `    <ExposeHeader>${h}</ExposeHeader>`),
          ...(r.maxAgeSeconds !== undefined ? [`    <MaxAgeSeconds>${r.maxAgeSeconds}</MaxAgeSeconds>`] : []),
          "  </CORSRule>",
        ].join("\n"),
      )
      .join("\n")}\n</CORSConfiguration>`;

  return (
    <div className="space-y-4">
      {drafts.length === 0 ? (
        <div className="flex flex-col items-center gap-3 rounded-md border border-dashed border-line px-4 py-8 text-center">
          <Code2 className="size-8 text-faint" />
          <p className="text-sm">No CORS rules: browsers on other sites cannot call this bucket.</p>
          <Button icon={<Plus className="size-4" />} onClick={() => setDrafts([newDraft()])}>
            Add rule
          </Button>
        </div>
      ) : (
        <div className="space-y-3">
          {drafts.map((d, i) => {
            const err = problem(d);
            return (
              <div key={d.key} className="space-y-3 rounded-md border border-line p-4">
                <div className="flex items-center justify-between">
                  <span className="label-strip">rule {i + 1}{i === 0 && drafts.length > 1 ? " · checked first" : ""}</span>
                  <IconButton label={`Remove rule ${i + 1}`} onClick={() => setDrafts((ds) => ds.filter((x) => x.key !== d.key))}>
                    <X className="size-4" />
                  </IconButton>
                </div>
                <Field label="Allowed origins" hint="Comma-separated. One * per origin: https://*.example.com, or * for any site.">
                  <Input mono value={d.origins} onChange={(e) => update(d.key, { origins: e.target.value })} placeholder="https://app.example.com" />
                </Field>
                <div>
                  <div className="label-strip mb-1.5">allowed methods</div>
                  <div className="flex flex-wrap gap-2" role="group" aria-label="Allowed methods">
                    {HTTP_METHODS.map((m) => (
                      <button
                        key={m}
                        type="button"
                        aria-pressed={d.methods.includes(m)}
                        onClick={() => toggleMethod(d, m)}
                        className={cx(
                          "h-7 rounded-md border px-2.5 font-mono text-xs transition-colors",
                          d.methods.includes(m) ? "border-accent bg-accent-soft text-accent-ink" : "border-line-strong text-muted hover:bg-sunken",
                        )}
                      >
                        {m}
                      </button>
                    ))}
                  </div>
                </div>
                <div className="grid gap-3 sm:grid-cols-[1fr_1fr_8rem]">
                  <Field label="Allowed headers" hint="Request headers the page may send; * allows any.">
                    <Input mono value={d.headers} onChange={(e) => update(d.key, { headers: e.target.value })} placeholder="*" />
                  </Field>
                  <Field label="Exposed headers" hint="Response headers the page may read.">
                    <Input mono value={d.expose} onChange={(e) => update(d.key, { expose: e.target.value })} placeholder="ETag" />
                  </Field>
                  <Field label="Max age" hint="Seconds.">
                    <Input mono value={d.maxAge} onChange={(e) => update(d.key, { maxAge: e.target.value })} inputMode="numeric" placeholder="—" />
                  </Field>
                </div>
                {err && <p className="text-xs text-danger">{err}</p>}
              </div>
            );
          })}
        </div>
      )}
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex gap-2">
          {drafts.length > 0 && (
            <Button variant="ghost" icon={<Plus className="size-4" />} onClick={() => setDrafts((ds) => [...ds, newDraft()])}>
              Add rule
            </Button>
          )}
          {drafts.length > 0 && (
            <Button variant="ghost" icon={<Code2 className="size-4" />} onClick={() => setShowXML((v) => !v)}>
              {showXML ? "Hide" : "Show"} S3 XML
            </Button>
          )}
        </div>
        <Button variant="primary" busy={busy} disabled={!dirty || invalid} icon={<Save className="size-4" />} onClick={save}>
          {drafts.length === 0 && bucket.cors.length > 0 ? "Remove CORS" : "Save CORS"}
        </Button>
      </div>
      {showXML && drafts.length > 0 && (
        <div className="space-y-1.5">
          <pre className="max-h-60 overflow-auto rounded-md border border-line bg-sunken/40 p-3 font-mono text-xs">{xml()}</pre>
          <p className="text-xs text-muted">The same rules in S3's XML format, as S3 clients read them with GetBucketCors.</p>
        </div>
      )}
    </div>
  );
}

// --- lifecycle rules (client-side planning) -------------------------------------------

interface LifecycleRule {
  id: number;
  prefix: string;
  expirationDays: number;
  abortMultipart: boolean;
  abortDays: number;
  enabled: boolean;
}

let lifecycleId = 0;

function LifecycleEditor({ bucket }: { bucket: Bucket }) {
  const [rules, setRules] = useState<LifecycleRule[]>([]);
  const [showSnippet, setShowSnippet] = useState(false);
  const toast = useToast();

  function addRule() {
    setRules((rs) => [...rs, {
      id: ++lifecycleId,
      prefix: "",
      expirationDays: 30,
      abortMultipart: true,
      abortDays: 7,
      enabled: true,
    }]);
  }

  function updateRule(id: number, patch: Partial<LifecycleRule>) {
    setRules((rs) => rs.map((r) => (r.id === id ? { ...r, ...patch } : r)));
  }

  function removeRule(id: number) {
    setRules((rs) => rs.filter((r) => r.id !== id));
  }

  function generateXML() {
    const rulesXml = rules.map((r, i) => {
      let xml = `  <Rule>
    <ID>rule-${i + 1}</ID>
    <Status>${r.enabled ? "Enabled" : "Disabled"}</Status>
    <Filter><Prefix>${r.prefix}</Prefix></Filter>`;
      if (r.expirationDays > 0) {
        xml += `\n    <Expiration><Days>${r.expirationDays}</Days></Expiration>`;
      }
      if (r.abortMultipart) {
        xml += `\n    <AbortIncompleteMultipartUpload><DaysAfterInitiation>${r.abortDays}</DaysAfterInitiation></AbortIncompleteMultipartUpload>`;
      }
      xml += `\n  </Rule>`;
      return xml;
    }).join("\n");
    return `<LifecycleConfiguration>\n${rulesXml}\n</LifecycleConfiguration>`;
  }

  function copyXML() {
    navigator.clipboard.writeText(generateXML()).then(() => toast("ok", "Lifecycle XML copied."));
  }

  return (
    <div className="space-y-4">
      {rules.length === 0 ? (
        <div className="flex flex-col items-center gap-3 rounded-md border border-dashed border-line py-8 text-muted">
          <Clock className="size-8 text-faint" />
          <p className="text-sm">No lifecycle rules</p>
          <p className="text-xs text-faint max-w-sm text-center">
            Create rules to automatically expire old objects or clean up incomplete uploads.
          </p>
          <Button icon={<Plus className="size-4" />} onClick={addRule}>
            Add Lifecycle Rule
          </Button>
        </div>
      ) : (
        <>
          <div className="space-y-3">
            {rules.map((rule, idx) => (
              <div key={rule.id} className="rounded-md border border-line bg-sunken/30 p-4 space-y-3">
                <div className="flex items-center justify-between">
                  <div className="flex items-center gap-2">
                    <span className="text-xs font-semibold text-ink">Rule {idx + 1}</span>
                    <button
                      onClick={() => updateRule(rule.id, { enabled: !rule.enabled })}
                      className={cx(
                        "rounded-full px-2 py-0.5 text-[10px] font-medium transition-colors",
                        rule.enabled
                          ? "bg-ok/10 text-ok"
                          : "bg-sunken text-faint",
                      )}
                    >
                      {rule.enabled ? "Enabled" : "Disabled"}
                    </button>
                  </div>
                  <IconButton label="Remove rule" onClick={() => removeRule(rule.id)}>
                    <X className="size-4" />
                  </IconButton>
                </div>

                <div className="grid gap-3 sm:grid-cols-2">
                  <Field label="Prefix filter" hint="Leave empty to apply to all objects">
                    <Input
                      mono
                      value={rule.prefix}
                      onChange={(e) => updateRule(rule.id, { prefix: e.target.value })}
                      placeholder="logs/ or temp/"
                    />
                  </Field>
                  <Field label="Expire objects after (days)">
                    <Input
                      mono
                      value={String(rule.expirationDays)}
                      onChange={(e) => updateRule(rule.id, { expirationDays: Number(e.target.value) || 0 })}
                      className="w-28"
                      inputMode="numeric"
                    />
                  </Field>
                </div>

                <div className="flex items-center gap-3">
                  <label className="flex items-center gap-2 text-xs">
                    <input
                      type="checkbox"
                      name="abort-multipart"
                      checked={rule.abortMultipart}
                      onChange={(e) => updateRule(rule.id, { abortMultipart: e.target.checked })}
                      className="size-4 accent-[var(--accent)]"
                    />
                    Abort incomplete multipart uploads after
                  </label>
                  {rule.abortMultipart && (
                    <div className="flex items-center gap-1">
                      <Input
                        mono
                        value={String(rule.abortDays)}
                        onChange={(e) => updateRule(rule.id, { abortDays: Number(e.target.value) || 1 })}
                        className="w-16 text-center"
                        inputMode="numeric"
                      />
                      <span className="text-xs text-muted">days</span>
                    </div>
                  )}
                </div>
              </div>
            ))}
          </div>
          <div className="flex gap-2">
            <Button icon={<Plus className="size-4" />} onClick={addRule} variant="ghost">
              Add Rule
            </Button>
            <Button icon={<Code2 className="size-4" />} onClick={() => setShowSnippet((v) => !v)}>
              {showSnippet ? "Hide" : "Show"} S3 XML
            </Button>
          </div>
          {showSnippet && (
            <div className="space-y-2">
              <div className="flex items-center justify-between">
                <span className="label-strip text-[10px]">S3 Lifecycle XML</span>
                <button
                  onClick={copyXML}
                  className="flex items-center gap-1 rounded px-2 py-0.5 text-[10px] font-medium text-muted hover:bg-sunken hover:text-ink transition-colors"
                >
                  Copy
                </button>
              </div>
              <pre className="max-h-60 overflow-auto rounded-md border border-line bg-surface p-3 font-mono text-xs text-ink">
                {generateXML()}
              </pre>
              <p className="text-[11px] text-muted">
                Apply with: <code className="font-mono bg-sunken px-1 rounded">aws s3api put-bucket-lifecycle-configuration --bucket {bucket.name} --lifecycle-configuration file://lifecycle.xml</code>
              </p>
            </div>
          )}
        </>
      )}
    </div>
  );
}
