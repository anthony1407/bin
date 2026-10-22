import { useEffect, useMemo, useState, type FormEvent } from "react";
import { Link, useNavigate } from "react-router";
import { ChevronDown, Globe, Lock, Plus, Search, SlidersHorizontal } from "lucide-react";
import { api, type Bucket } from "../api";
import { bytes, count, date } from "../lib/format";
import { Badge, Button, EmptyState, Field, Input, Modal, PageHeader, Spinner, errorText, useToast } from "../components/ui";

const NAME_RULES = [
  "3 to 63 characters",
  "lowercase letters, digits, dots and hyphens",
  "starts and ends with a letter or digit",
  "no two dots in a row, not an IP address",
];

function nameProblem(name: string): string {
  if (!name) return "";
  if (name.length < 3 || name.length > 63) return "Use 3 to 63 characters.";
  if (!/^[a-z0-9.-]+$/.test(name)) return "Only lowercase letters, digits, dots and hyphens.";
  if (!/^[a-z0-9]/.test(name) || !/[a-z0-9]$/.test(name)) return "Start and end with a letter or digit.";
  if (name.includes("..")) return "No two dots in a row.";
  if (/^\d+\.\d+\.\d+\.\d+$/.test(name)) return "A bucket name cannot look like an IP address.";
  return "";
}

export function accessLabel(b: Bucket) {
  if (b.policy.mode === "public") return { tone: "warn" as const, text: "public", icon: Globe };
  if (b.policy.mode === "custom") return { tone: "accent" as const, text: "custom", icon: SlidersHorizontal };
  return { tone: "neutral" as const, text: "private", icon: Lock };
}

function BucketCard({ b }: { b: Bucket }) {
  const access = accessLabel(b);
  const quotaUse = b.quotaBytes > 0 ? Math.min(1, b.size / b.quotaBytes) : 0;
  return (
    <Link
      to={`/buckets/${encodeURIComponent(b.name)}`}
      className="group relative flex flex-col rounded-card border border-line bg-surface transition hover:-translate-y-0.5 hover:border-line-strong hover:shadow-md"
    >
      <div className="flex items-start gap-3 p-5 pb-4">
        <div className="min-w-0 flex-1">
          <div className="truncate font-mono text-[15px] font-medium text-ink group-hover:text-accent-ink">{b.name}</div>
          <div className="mt-1 text-xs text-muted">Created {date(b.createdAt)}</div>
        </div>
        <Badge tone={access.tone}>
          <access.icon className="size-3" />
          {access.text}
        </Badge>
      </div>
      <div className="mt-auto grid grid-cols-2 border-t border-dashed border-line-strong">
        <div className="px-5 py-3">
          <div className="label-strip">objects</div>
          <div className="num mt-0.5 text-lg text-ink">{count(b.objects)}</div>
        </div>
        <div className="border-l border-dashed border-line-strong px-5 py-3">
          <div className="label-strip">size</div>
          <div className="num mt-0.5 text-lg text-ink">{bytes(b.size)}</div>
        </div>
      </div>
      {b.quotaBytes > 0 && (
        <div className="border-t border-line px-5 py-2.5">
          <div className="label-strip mb-1.5 flex justify-between">
            <span>quota</span>
            <span>
              {bytes(b.size)} / {bytes(b.quotaBytes)}
            </span>
          </div>
          <div className="h-1.5 overflow-hidden rounded-full bg-sunken">
            <div
              className={quotaUse > 0.9 ? "h-full bg-danger" : quotaUse > 0.75 ? "h-full bg-warn" : "h-full bg-accent"}
              style={{ width: `${Math.max(2, quotaUse * 100)}%` }}
            />
          </div>
        </div>
      )}
    </Link>
  );
}

function CreateBucketModal({ open, onClose, onCreated }: { open: boolean; onClose: () => void; onCreated: (b: Bucket) => void }) {
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [rules, setRules] = useState(false);
  const problem = nameProblem(name);

  useEffect(() => {
    if (open) {
      setName("");
      setError("");
    }
  }, [open]);

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (problem) return;
    setBusy(true);
    setError("");
    try {
      onCreated(await api.createBucket(name));
    } catch (err) {
      setError(errorText(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal
      open={open}
      onClose={onClose}
      title="Create bucket"
      footer={
        <>
          <Button onClick={onClose}>Cancel</Button>
          <Button variant="primary" busy={busy} disabled={!name || !!problem} onClick={submit}>
            Create bucket
          </Button>
        </>
      }
    >
      <form onSubmit={submit} className="space-y-4">
        <Field label="Bucket name" error={problem || error} hint="This is part of every object URL and cannot be changed later.">
          <Input mono autoFocus value={name} onChange={(e) => setName(e.target.value.trim())} placeholder="photos-2026" />
        </Field>
        <button type="button" onClick={() => setRules(!rules)} className="flex items-center gap-1 text-xs font-medium text-muted hover:text-ink">
          <ChevronDown className={rules ? "size-3.5 rotate-180 transition" : "size-3.5 transition"} />
          Naming rules
        </button>
        {rules && (
          <ul className="space-y-1 rounded-md bg-sunken px-4 py-3 text-xs text-muted">
            {NAME_RULES.map((r) => (
              <li key={r}>· {r}</li>
            ))}
          </ul>
        )}
        <p className="text-xs text-muted">
          New buckets are <strong className="text-ink">private</strong>. Quota, tags and public access can be set afterwards in the bucket settings.
        </p>
      </form>
    </Modal>
  );
}

export function BucketsPage() {
  const [buckets, setBuckets] = useState<Bucket[] | null>(null);
  const [filter, setFilter] = useState("");
  const [creating, setCreating] = useState(false);
  const toast = useToast();
  const navigate = useNavigate();

  useEffect(() => {
    api.buckets().then(setBuckets).catch((e) => toast("error", errorText(e)));
  }, [toast]);

  const shown = useMemo(() => (buckets ?? []).filter((b) => b.name.includes(filter.toLowerCase())), [buckets, filter]);
  const totals = useMemo(
    () => (buckets ?? []).reduce((t, b) => ({ objects: t.objects + b.objects, size: t.size + b.size }), { objects: 0, size: 0 }),
    [buckets],
  );

  return (
    <>
      <PageHeader
        title="Buckets"
        subtitle={
          buckets && (
            <span className="num">
              {count(buckets.length)} buckets · {count(totals.objects)} objects · {bytes(totals.size)}
            </span>
          )
        }
        actions={
          <Button variant="primary" icon={<Plus className="size-4" />} onClick={() => setCreating(true)}>
            Create bucket
          </Button>
        }
      />

      {buckets === null ? (
        <div className="flex justify-center py-20">
          <Spinner />
        </div>
      ) : buckets.length === 0 ? (
        <div className="rounded-card border border-dashed border-line-strong bg-surface">
          <EmptyState
            title="No buckets yet"
            body="A bucket is a top-level container for your files. Create one here, or with aws s3 mb from any S3 client."
            action={
              <Button variant="primary" icon={<Plus className="size-4" />} onClick={() => setCreating(true)}>
                Create your first bucket
              </Button>
            }
          />
        </div>
      ) : (
        <>
          {buckets.length > 6 && (
            <div className="relative mb-4 max-w-xs">
              <Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-faint" />
              <Input value={filter} onChange={(e) => setFilter(e.target.value)} placeholder="Filter buckets" className="pl-9" />
            </div>
          )}
          <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
            {shown.map((b) => (
              <BucketCard key={b.name} b={b} />
            ))}
          </div>
        </>
      )}

      <CreateBucketModal
        open={creating}
        onClose={() => setCreating(false)}
        onCreated={(b) => {
          setCreating(false);
          toast("ok", `Bucket ${b.name} created.`);
          navigate(`/buckets/${encodeURIComponent(b.name)}`);
        }}
      />
    </>
  );
}
