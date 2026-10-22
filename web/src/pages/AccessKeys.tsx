import { useEffect, useState } from "react";
import { Check, Copy, KeyRound, Pencil, Plus, Trash2, TriangleAlert } from "lucide-react";
import { api, type AccessKey, type Bucket } from "../api";
import { date, relative } from "../lib/format";
import { Badge, Button, EmptyState, Field, IconButton, Input, Modal, PageHeader, Spinner, cx, errorText, useToast } from "../components/ui";

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

// --- create / edit form -----------------------------------------------------------------

interface Draft {
  name: string;
  expiresAt: string; // YYYY-MM-DD or ""
  buckets: string[];
  readOnly: boolean;
}

function KeyForm({ draft, setDraft, buckets }: { draft: Draft; setDraft: (d: Draft) => void; buckets: Bucket[] }) {
  const all = draft.buckets.length === 0;
  const toggle = (name: string) =>
    setDraft({ ...draft, buckets: draft.buckets.includes(name) ? draft.buckets.filter((b) => b !== name) : [...draft.buckets, name] });
  return (
    <div className="space-y-5">
      <Field label="Name" hint="What this key is for, e.g. the app or person using it.">
        <Input autoFocus value={draft.name} onChange={(e) => setDraft({ ...draft, name: e.target.value })} placeholder="backup job" />
      </Field>
      <Field label="Expires" hint="Leave empty for a key that never expires.">
        <Input type="date" value={draft.expiresAt} min={new Date().toISOString().slice(0, 10)} onChange={(e) => setDraft({ ...draft, expiresAt: e.target.value })} className="w-48" />
      </Field>
      <div>
        <div className="mb-1.5 text-sm font-medium">Buckets</div>
        <div className="rounded-md border border-line-strong">
          <label className="flex items-center gap-2.5 border-b border-line px-3 py-2 text-sm">
            <input type="checkbox" checked={all} onChange={() => setDraft({ ...draft, buckets: all ? buckets.slice(0, 1).map((b) => b.name) : [] })} className="size-4 accent-[var(--accent)]" />
            All buckets, including future ones
          </label>
          <div className={cx("max-h-40 overflow-y-auto", all && "opacity-50")}>
            {buckets.length === 0 && <div className="px-3 py-2 text-xs text-faint">No buckets yet.</div>}
            {buckets.map((b) => (
              <label key={b.name} className="flex items-center gap-2.5 px-3 py-1.5 font-mono text-xs">
                <input type="checkbox" disabled={all} checked={all || draft.buckets.includes(b.name)} onChange={() => toggle(b.name)} className="size-4 accent-[var(--accent)]" />
                {b.name}
              </label>
            ))}
          </div>
        </div>
      </div>
      <label className="flex items-start gap-2.5 text-sm">
        <input type="checkbox" checked={draft.readOnly} onChange={(e) => setDraft({ ...draft, readOnly: e.target.checked })} className="mt-0.5 size-4 accent-[var(--accent)]" />
        <span>
          Read-only
          <span className="block text-xs text-muted">Can list and download, but not upload, delete or create buckets.</span>
        </span>
      </label>
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

const emptyDraft: Draft = { name: "", expiresAt: "", buckets: [], readOnly: false };

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
    setDraft({ name: k.name, expiresAt: k.expiresAt ? k.expiresAt.slice(0, 10) : "", buckets: k.buckets, readOnly: k.readOnly });
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
                        {k.buckets.length === 0 ? (
                          <span className="text-xs">All buckets</span>
                        ) : (
                          k.buckets.map((b) => (
                            <span key={b} className="rounded bg-sunken px-1.5 py-0.5 font-mono text-[11px]">
                              {b}
                            </span>
                          ))
                        )}
                        {k.readOnly && <Badge tone="accent">read-only</Badge>}
                      </div>
                    </td>
                    <td className="hidden py-3 pr-3 text-xs lg:table-cell">{k.expiresAt ? date(k.expiresAt) : <span className="text-faint">Never</span>}</td>
                    <td className="hidden py-3 pr-3 text-xs sm:table-cell">{k.lastUsedAt ? relative(k.lastUsedAt) : <span className="text-faint">Never</span>}</td>
                    <td className="py-3 pr-3">
                      <div className="flex items-center justify-end gap-1">
                        <label className="relative mr-1 inline-flex cursor-pointer items-center" title={k.enabled ? "Disable" : "Enable"}>
                          <input type="checkbox" className="peer sr-only" checked={k.enabled} onChange={(e) => setEnabled(k, e.target.checked)} aria-label={`Enabled ${k.accessKey}`} />
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
        title={created ? "Access key created" : editing ? "Edit access key" : "Create access key"}
        wide={!!created}
        footer={
          created ? (
            <Button variant="primary" onClick={() => setFormOpen(false)}>
              I saved the secret
            </Button>
          ) : (
            <>
              <Button onClick={() => setFormOpen(false)}>Cancel</Button>
              <Button variant="primary" busy={busy} onClick={submit}>
                {editing ? "Save changes" : "Create key"}
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
