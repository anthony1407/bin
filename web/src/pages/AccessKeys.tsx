import { useEffect, useState } from "react";
import { Check, ChevronDown, ChevronRight, Copy, KeyRound, Pencil, Plus, Trash2, TriangleAlert } from "lucide-react";
import Editor from "../components/CodeEditor";
import { api, type AccessKey, type Bucket } from "../api";
import { date, relative } from "../lib/format";
import { Badge, Button, EmptyState, Field, IconButton, Input, Modal, PageHeader, Spinner, cx, errorText, useToast } from "../components/ui";
import { DatePicker } from "../components/DatePicker";

function CopyButton({ text, label }: { text: string; label: string }) {
  const [done, setDone] = useState(false);
  return (
    <IconButton
      label={label}
      onClick={() =>
        navigator.clipboard.writeText(text).then(() => {
          setDone(true);
          setTimeout(() => setDone(false), 1500);
        })
      }
    >
      {done ? <Check className="size-4 text-ok" /> : <Copy className="size-4" />}
    </IconButton>
  );
}

function status(k: AccessKey): { tone: "ok" | "neutral" | "danger"; text: string } {
  if (k.expired) return { tone: "danger", text: "expired" };
  if (!k.enabled) return { tone: "neutral", text: "disabled" };
  return { tone: "ok", text: "active" };
}

// --- policy helpers ---------------------------------------------------------------------

function generatePolicyJson(buckets: string[], readOnly: boolean): string {
  const actions = readOnly ? ["s3:Get*", "s3:List*"] : ["s3:*"];
  const resources =
    buckets.length === 0
      ? ["arn:aws:s3:::*"]
      : buckets.flatMap((b) => [`arn:aws:s3:::${b}`, `arn:aws:s3:::${b}/*`]);

  return JSON.stringify(
    {
      Version: "2012-10-17",
      Statement: [
        {
          Effect: "Allow",
          Action: actions,
          Resource: resources,
        },
      ],
    },
    null,
    2,
  );
}

function parsePolicyJson(json: string): { buckets?: string[]; readOnly?: boolean } | null {
  try {
    const data = JSON.parse(json);
    const stmts = Array.isArray(data.Statement) ? data.Statement : [data.Statement];
    let isReadOnly = false;
    let allBuckets = false;
    const bucketSet = new Set<string>();

    for (const s of stmts) {
      if (!s) continue;
      const acts: string[] = Array.isArray(s.Action) ? s.Action : [s.Action];
      if (acts.every((a) => typeof a === "string" && (a.startsWith("s3:Get") || a.startsWith("s3:List")))) {
        isReadOnly = true;
      }
      const reses: string[] = Array.isArray(s.Resource) ? s.Resource : [s.Resource];
      for (const r of reses) {
        if (typeof r !== "string") continue;
        if (r === "arn:aws:s3:::*" || r === "*") {
          allBuckets = true;
        } else if (r.startsWith("arn:aws:s3:::")) {
          const b = r.replace("arn:aws:s3:::", "").split("/")[0];
          if (b && b !== "*") bucketSet.add(b);
        }
      }
    }
    return {
      buckets: allBuckets ? [] : Array.from(bucketSet),
      readOnly: isReadOnly,
    };
  } catch {
    return null;
  }
}

// --- create / edit form (with the Monaco editor) ---------------------------------------

interface Draft {
  name: string;
  description: string;
  expiresAt: string; // YYYY-MM-DD or ""
  buckets: string[];
  readOnly: boolean;
  enabled: boolean;
  policy: string;
}

function KeyForm({ draft, setDraft, buckets }: { draft: Draft; setDraft: (d: Draft) => void; buckets: Bucket[] }) {
  const [copiedPolicy, setCopiedPolicy] = useState(false);
  const [showBucketPicker, setShowBucketPicker] = useState(false);
  const isNever = !draft.expiresAt;
  const all = draft.buckets.length === 0;

  const copyPolicy = () => {
    navigator.clipboard.writeText(draft.policy).then(() => {
      setCopiedPolicy(true);
      setTimeout(() => setCopiedPolicy(false), 1500);
    });
  };

  const handleEditorChange = (val: string | undefined) => {
    const nextVal = val ?? "";
    const parsed = parsePolicyJson(nextVal);
    if (parsed) {
      setDraft({
        ...draft,
        policy: nextVal,
        ...(parsed.buckets !== undefined ? { buckets: parsed.buckets } : {}),
        ...(parsed.readOnly !== undefined ? { readOnly: parsed.readOnly } : {}),
      });
    } else {
      setDraft({ ...draft, policy: nextVal });
    }
  };

  const toggleBucket = (name: string) => {
    const nextBuckets = draft.buckets.includes(name)
      ? draft.buckets.filter((b) => b !== name)
      : [...draft.buckets, name];
    setDraft({
      ...draft,
      buckets: nextBuckets,
      policy: generatePolicyJson(nextBuckets, draft.readOnly),
    });
  };

  const setAllBuckets = (selectAll: boolean) => {
    const nextBuckets = selectAll ? [] : buckets.slice(0, 1).map((b) => b.name);
    setDraft({
      ...draft,
      buckets: nextBuckets,
      policy: generatePolicyJson(nextBuckets, draft.readOnly),
    });
  };

  return (
    <div className="space-y-4">
      {/* 1. Access Key Policy with Monaco Editor */}
      <div>
        <div className="mb-1.5 flex items-center justify-between">
          <span className="text-sm font-medium text-ink">Access Key Policy</span>
          <button
            type="button"
            onClick={() => setShowBucketPicker((p) => !p)}
            className="flex items-center gap-1 text-xs text-muted hover:text-ink transition-colors"
          >
            <span>{all ? "All buckets" : `${draft.buckets.length} bucket(s)`}</span>
            {showBucketPicker ? <ChevronDown className="size-3.5" /> : <ChevronRight className="size-3.5" />}
          </button>
        </div>

        {/* Optional Collapsible Bucket / ReadOnly Helper */}
        {showBucketPicker && (
          <div className="mb-2.5 rounded-md border border-line bg-sunken/40 p-3 text-xs space-y-2.5">
            <div className="flex items-center justify-between">
              <label className="flex items-center gap-2 font-medium">
                <input
                  type="checkbox"
                  name="all-buckets"
                  checked={all}
                  onChange={(e) => setAllBuckets(e.target.checked)}
                  className="size-4 accent-[var(--accent)]"
                />
                All buckets (full access)
              </label>
              <label className="flex items-center gap-2 font-medium">
                <input
                  type="checkbox"
                  name="read-only"
                  checked={draft.readOnly}
                  onChange={(e) => {
                    const ro = e.target.checked;
                    setDraft({
                      ...draft,
                      readOnly: ro,
                      policy: generatePolicyJson(draft.buckets, ro),
                    });
                  }}
                  className="size-4 accent-[var(--accent)]"
                />
                Read-only
              </label>
            </div>
            {!all && (
              <div className="max-h-28 overflow-y-auto space-y-1 pt-1 border-t border-line">
                {buckets.map((b) => (
                  <label key={b.name} className="flex items-center gap-2 font-mono">
                    <input
                      type="checkbox"
                      name="buckets"
                      checked={draft.buckets.includes(b.name)}
                      onChange={() => toggleBucket(b.name)}
                      className="size-3.5 accent-[var(--accent)]"
                    />
                    {b.name}
                  </label>
                ))}
              </div>
            )}
          </div>
        )}

        {/* Monaco Editor Container */}
        <div className="relative overflow-hidden rounded-md border border-line-strong bg-[#1e1e1e] shadow-inner">
          <Editor
            height="210px"
            defaultLanguage="json"
            language="json"
            theme="vs-dark"
            value={draft.policy}
            onChange={handleEditorChange}
            options={{
              minimap: { enabled: false },
              scrollBeyondLastLine: false,
              fontSize: 12,
              tabSize: 2,
              lineNumbers: "on",
              folding: true,
              wordWrap: "on",
              automaticLayout: true,
              formatOnPaste: true,
            }}
          />
          {/* Copy Policy Button on bottom-right inside editor */}
          <button
            type="button"
            onClick={copyPolicy}
            title="Copy policy to clipboard"
            aria-label="Copy policy"
            className="absolute right-2.5 bottom-2.5 z-10 flex size-7 items-center justify-center rounded border border-white/10 bg-[#252526]/90 text-slate-300 shadow-sm backdrop-blur transition-colors hover:bg-[#333333] hover:text-white focus:outline-none"
          >
            {copiedPolicy ? <Check className="size-3.5 text-ok" /> : <Copy className="size-3.5" />}
          </button>
        </div>
      </div>

      {/* 2. Expiry */}
      <div>
        <div className="mb-1.5 text-sm font-medium text-ink">Expiry</div>
        <div className="space-y-2">
          <DatePicker
            value={draft.expiresAt}
            onChange={(val) => setDraft({ ...draft, expiresAt: val })}
            min={new Date().toISOString().slice(0, 10)}
            placeholder="No expiration (never expires)"
            disabled={isNever}
            className="w-full"
          />
          <label className="flex items-center gap-2.5 text-xs text-muted cursor-pointer select-none">
            <input
              type="checkbox"
              name="no-expiry"
              checked={isNever}
              onChange={(e) => {
                if (e.target.checked) {
                  setDraft({ ...draft, expiresAt: "" });
                } else {
                  const d = new Date();
                  d.setDate(d.getDate() + 90);
                  setDraft({ ...draft, expiresAt: d.toISOString().slice(0, 10) });
                }
              }}
              className="size-4 accent-[var(--accent)]"
            />
            <span>Never expires</span>
          </label>
        </div>
      </div>

      {/* 3. Name */}
      <Field label="Name">
        <Input
          value={draft.name}
          onChange={(e) => setDraft({ ...draft, name: e.target.value })}
          placeholder="Enter a name"
        />
      </Field>

      {/* 4. Description */}
      <Field label="Description">
        <Input
          value={draft.description}
          onChange={(e) => setDraft({ ...draft, description: e.target.value })}
          placeholder="Enter a description"
        />
      </Field>

      {/* 5. Status (switch toggle) */}
      <div className="flex items-center justify-between pt-1">
        <span className="text-sm font-medium text-ink">Status</span>
        <label className="flex cursor-pointer items-center gap-2.5 select-none text-xs">
          <span className={cx(!draft.enabled ? "font-medium text-ink" : "text-muted")}>Disabled</span>
          <div
            onClick={() => setDraft({ ...draft, enabled: !draft.enabled })}
            className={cx(
              "relative flex h-5 w-9 shrink-0 items-center rounded-full transition-colors",
              draft.enabled ? "bg-ok" : "bg-line-strong",
            )}
          >
            <span
              className={cx(
                "absolute size-3.5 rounded-full bg-white shadow transition-transform",
                draft.enabled ? "translate-x-4" : "translate-x-0.5",
              )}
            />
          </div>
          <span className={cx(draft.enabled ? "font-medium text-ink" : "text-muted")}>Enabled</span>
        </label>
      </div>
    </div>
  );
}

function Created({ k }: { k: AccessKey }) {
  const [server, setServer] = useState({ endpoint: `${location.protocol}//${location.hostname}:9000`, region: "us-east-1" });
  useEffect(() => {
    api
      .info()
      .then((i) => setServer({ endpoint: i.s3Endpoint, region: i.region }))
      .catch(() => {});
  }, []);
  // A named profile keeps the commands the same in bash, PowerShell and cmd.
  const snippet = `aws configure set aws_access_key_id ${k.accessKey} --profile bin
aws configure set aws_secret_access_key ${k.secretKey} --profile bin
aws configure set region ${server.region} --profile bin
aws configure set endpoint_url ${server.endpoint} --profile bin
aws --profile bin s3 ls`;
  return (
    <div className="space-y-4">
      <div className="flex gap-2.5 rounded-md border border-warn/30 bg-warn-soft px-3.5 py-2.5 text-sm text-warn">
        <TriangleAlert className="mt-0.5 size-4 shrink-0" />
        Copy the secret key now. It is stored encrypted and will not be shown again.
      </div>
      {[
        ["Access key", k.accessKey],
        ["Secret key", k.secretKey ?? ""],
      ].map(([label, value]) => (
        <div key={label}>
          <div className="mb-1.5 text-sm font-medium">{label}</div>
          <div className="flex items-center gap-1 rounded-md border border-line-strong bg-sunken pl-3">
            <code className="flex-1 truncate font-mono text-sm">{value}</code>
            <CopyButton text={value} label={`Copy ${label.toLowerCase()}`} />
          </div>
        </div>
      ))}
      <div>
        <div className="mb-1.5 flex items-center justify-between text-sm font-medium">
          Try it with the AWS CLI
          <CopyButton text={snippet} label="Copy commands" />
        </div>
        <pre className="rounded-md break-all whitespace-pre-wrap border border-transparent bg-night p-3 font-mono text-xs leading-relaxed text-slate-200 dark:border-line-strong">{snippet}</pre>
      </div>
    </div>
  );
}

// --- page ------------------------------------------------------------------------------------

const emptyDraft: Draft = {
  name: "",
  description: "",
  expiresAt: "",
  buckets: [],
  readOnly: false,
  enabled: true,
  policy: generatePolicyJson([], false),
};

export function AccessKeysPage() {
  const [keys, setKeys] = useState<AccessKey[] | null>(null);
  const [buckets, setBuckets] = useState<Bucket[]>([]);
  const [formOpen, setFormOpen] = useState(false);
  const [editing, setEditing] = useState<AccessKey | null>(null);
  const [draft, setDraft] = useState<Draft>(emptyDraft);
  const [created, setCreated] = useState<AccessKey | null>(null);
  const [deleting, setDeleting] = useState<AccessKey | null>(null);
  const [removing, setRemoving] = useState(false);
  const [busy, setBusy] = useState(false);
  const toast = useToast();

  const load = () => api.accessKeys().then(setKeys).catch((e) => toast("error", errorText(e)));
  useEffect(() => {
    load();
    api.buckets().then(setBuckets).catch(() => {});
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  function openCreate() {
    setEditing(null);
    setDraft(emptyDraft);
    setCreated(null);
    setFormOpen(true);
  }

  function openEdit(k: AccessKey) {
    setEditing(k);
    setDraft({
      name: k.name,
      description: "",
      expiresAt: k.expiresAt ? k.expiresAt.slice(0, 10) : "",
      buckets: k.buckets,
      readOnly: k.readOnly,
      enabled: k.enabled,
      policy: generatePolicyJson(k.buckets, k.readOnly),
    });
    setCreated(null);
    setFormOpen(true);
  }

  async function submit() {
    setBusy(true);
    // Expiry dates are inclusive: the key works until the end of that day (UTC).
    const expiresAt = draft.expiresAt ? `${draft.expiresAt}T23:59:59Z` : undefined;
    try {
      if (editing) {
        await api.updateAccessKey(editing.accessKey, {
          name: draft.name,
          enabled: draft.enabled,
          buckets: draft.buckets,
          readOnly: draft.readOnly,
          ...(expiresAt ? { expiresAt } : { noExpiry: true }),
        });
        setFormOpen(false);
        toast("ok", "Access key updated.");
      } else {
        setCreated(await api.createAccessKey({ name: draft.name, expiresAt, buckets: draft.buckets, readOnly: draft.readOnly }));
      }
      load();
    } catch (e) {
      toast("error", errorText(e));
    } finally {
      setBusy(false);
    }
  }

  async function setEnabled(k: AccessKey, enabled: boolean) {
    try {
      await api.updateAccessKey(k.accessKey, { enabled });
      load();
    } catch (e) {
      toast("error", errorText(e));
    }
  }

  async function remove() {
    if (!deleting) return;
    setRemoving(true);
    try {
      await api.deleteAccessKey(deleting.accessKey);
      toast("ok", `Access key ${deleting.accessKey} deleted.`);
      setDeleting(null);
      load();
    } catch (e) {
      toast("error", errorText(e));
    } finally {
      setRemoving(false);
    }
  }

  return (
    <>
      <PageHeader
        title="Access keys"
        subtitle="Credentials for S3 clients and apps. The root key from the environment is not listed here."
        actions={
          <Button variant="primary" icon={<Plus className="size-4" />} onClick={openCreate}>
            Create access key
          </Button>
        }
      />

      {keys === null ? (
        <div className="flex justify-center py-20">
          <Spinner />
        </div>
      ) : keys.length === 0 ? (
        <div className="rounded-card border border-dashed border-line-strong bg-surface">
          <EmptyState
            title="No access keys"
            body="Give each app or person its own key, limited to the buckets it needs, instead of sharing the root credentials."
            action={
              <Button variant="primary" icon={<KeyRound className="size-4" />} onClick={openCreate}>
                Create your first key
              </Button>
            }
          />
        </div>
      ) : (
        <div className="overflow-hidden rounded-card border border-line bg-surface">
          <table className="w-full text-sm">
            <thead className="border-b border-line bg-sunken/60 text-left">
              <tr className="label-strip">
                <th className="w-full py-2 pr-3 pl-4 font-normal md:w-[38%]">key</th>
                <th className="hidden py-2 pr-3 font-normal md:table-cell">scope</th>
                <th className="hidden py-2 pr-3 font-normal lg:table-cell">expires</th>
                <th className="hidden py-2 pr-3 font-normal sm:table-cell">last used</th>
                <th className="py-2 pr-4 font-normal">
                  <span className="sr-only">actions</span>
                </th>
              </tr>
            </thead>
            <tbody className="divide-y divide-line">
              {keys.map((k) => {
                const s = status(k);
                return (
                  <tr key={k.accessKey} className={cx(!k.enabled && "text-muted")}>
                    <td className="max-w-0 py-3 pr-3 pl-4">
                      <div className="flex items-center gap-2">
                        <span className="truncate font-medium">{k.name || "Untitled"}</span>
                        <Badge tone={s.tone}>{s.text}</Badge>
                      </div>
                      <div className="mt-0.5 truncate font-mono text-xs text-muted">{k.accessKey}</div>
                    </td>
                    <td className="hidden py-3 pr-3 md:table-cell">
                      <div className="flex flex-wrap items-center gap-1">
                        {k.groups.length > 0 ? (
                          // Groups replace the key's own scope.
                          k.groups.map((g) => (
                            <span key={g} className="rounded bg-accent-soft px-1.5 py-0.5 font-mono text-[11px] text-accent-ink" title="Access from this group">
                              group: {g}
                            </span>
                          ))
                        ) : k.buckets.length === 0 ? (
                          <span className="text-xs">All buckets</span>
                        ) : (
                          k.buckets.map((b) => (
                            <span key={b} className="rounded bg-sunken px-1.5 py-0.5 font-mono text-[11px]">
                              {b}
                            </span>
                          ))
                        )}
                        {k.groups.length === 0 && k.readOnly && <Badge tone="accent">read-only</Badge>}
                      </div>
                    </td>
                    <td className="hidden py-3 pr-3 text-xs lg:table-cell">{k.expiresAt ? date(k.expiresAt) : <span className="text-faint">Never</span>}</td>
                    <td className="hidden py-3 pr-3 text-xs sm:table-cell">{k.lastUsedAt ? relative(k.lastUsedAt) : <span className="text-faint">Never</span>}</td>
                    <td className="py-3 pr-3">
                      <div className="flex items-center justify-end gap-1">
                        <label className="relative mr-1 inline-flex cursor-pointer items-center" title={k.enabled ? "Disable" : "Enable"}>
                          <input type="checkbox" name="enabled" className="peer sr-only" checked={k.enabled} onChange={(e) => setEnabled(k, e.target.checked)} aria-label={`Enabled ${k.accessKey}`} />
                          <span className="h-5 w-9 rounded-full bg-line-strong transition-colors peer-checked:bg-ok peer-focus-visible:ring-2 peer-focus-visible:ring-accent/40" />
                          <span className="absolute left-0.5 size-4 rounded-full bg-surface shadow transition-transform peer-checked:translate-x-4" />
                        </label>
                        <IconButton label="Edit" onClick={() => openEdit(k)}>
                          <Pencil className="size-4" />
                        </IconButton>
                        <IconButton label="Delete" onClick={() => setDeleting(k)} className="hover:text-danger">
                          <Trash2 className="size-4" />
                        </IconButton>
                      </div>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}

      <Modal
        open={formOpen}
        onClose={() => setFormOpen(false)}
        title={
          created ? (
            "Access key created"
          ) : editing ? (
            <span className="flex items-center gap-2">
              <KeyRound className="size-4 text-accent" />
              <span>Edit details of - {editing.accessKey}</span>
            </span>
          ) : (
            <span className="flex items-center gap-2">
              <KeyRound className="size-4 text-accent" />
              <span>Create access key</span>
            </span>
          )
        }
        wide={true}
        footer={
          created ? (
            <Button variant="primary" onClick={() => setFormOpen(false)}>
              I saved the secret
            </Button>
          ) : (
            <>
              <Button onClick={() => setFormOpen(false)}>Cancel</Button>
              <Button variant="primary" busy={busy} onClick={submit}>
                {editing ? "Update" : "Create"}
              </Button>
            </>
          )
        }
      >
        {created ? <Created k={created} /> : <KeyForm draft={draft} setDraft={setDraft} buckets={buckets} />}
      </Modal>

      <Modal
        open={!!deleting}
        onClose={() => !removing && setDeleting(null)}
        title="Delete access key"
        footer={
          <>
            <Button disabled={removing} onClick={() => setDeleting(null)}>
              Cancel
            </Button>
            <Button variant="danger" icon={<Trash2 className="size-4" />} busy={removing} onClick={remove}>
              {removing ? "Deleting…" : "Delete key"}
            </Button>
          </>
        }
      >
        <p className="text-sm">
          Clients using <span className="font-mono">{deleting?.accessKey}</span> will stop working immediately. To pause it instead, turn it off with the switch.
        </p>
      </Modal>
    </>
  );
}
