import { useEffect, useMemo, useState, type FormEvent } from "react";
import { useNavigate } from "react-router";
import {
  Archive,
  ChevronDown,
  ChevronUp,
  Code2,
  Globe,
  Lock,
  Plus,
  RefreshCw,
  Search,
  Settings2,
  SlidersHorizontal,
  Trash2,
} from "lucide-react";
import { api, type Bucket } from "../api";
import { useAuth, useIsAdmin } from "../auth";
import { bytes, count } from "../lib/format";
import { Button, IconButton, Input, Modal, Spinner, cx, errorText, useToast } from "../components/ui";

const NAME_RULES = [
  "Bucket names must be between 3 and 63 characters long.",
  "Bucket names can consist only of lowercase letters, numbers, dots (.), and hyphens (-).",
  "Bucket names must begin and end with a letter or number.",
  "Bucket names must not contain two adjacent periods.",
  "Bucket names must not be formatted as an IP address.",
];

export function accessLabel(b: Bucket) {
  if (b.policy.mode === "public") return { tone: "warn" as const, text: "public", icon: Globe };
  if (b.policy.mode === "custom") return { tone: "accent" as const, text: "custom", icon: SlidersHorizontal };
  return { tone: "neutral" as const, text: "private", icon: Lock };
}

function validateBucketName(name: string): string {
  if (!name) return "";
  if (name.length < 3 || name.length > 63) return "Bucket name must be between 3 and 63 characters.";
  if (!/^[a-z0-9.-]+$/.test(name)) return "Only lowercase letters, numbers, dots and hyphens are allowed.";
  if (!/^[a-z0-9]/.test(name) || !/[a-z0-9]$/.test(name)) return "Must begin and end with a letter or number.";
  if (name.includes("..")) return "Cannot contain two adjacent periods.";
  if (/^\d+\.\d+\.\d+\.\d+$/.test(name)) return "Cannot be formatted as an IP address.";
  return "";
}

function bucketAccessLabel(b: Bucket): string {
  if (b.policy.mode === "public") return "PUBLIC READ";
  if (b.policy.mode === "custom") return "CUSTOM";
  return "PRIVATE";
}

function CreateBucketModal({
  open,
  onClose,
  onCreated,
  admin,
}: {
  open: boolean;
  onClose: () => void;
  onCreated: (b: Bucket) => void;
  /** Users only name the bucket: access, versioning and quota are admin settings. */
  admin: boolean;
}) {
  const [name, setName] = useState("");
  const [showRules, setShowRules] = useState(false);
  const [versioning, setVersioning] = useState(false);
  const [quota, setQuota] = useState(false);
  const [quotaAmount, setQuotaAmount] = useState("1");
  const [quotaUnit, setQuotaUnit] = useState<"GiB" | "TiB">("TiB");
  const [isPublic, setIsPublic] = useState(false);
  const [busy, setBusy] = useState(false);
  const toast = useToast();

  const nameError = validateBucketName(name);

  useEffect(() => {
    if (open) {
      setName("");
      setVersioning(false);
      setQuota(false);
      setIsPublic(false);
      setShowRules(false);
    }
  }, [open]);

  function handleClear() {
    setName("");
    setVersioning(false);
    setQuota(false);
    setIsPublic(false);
  }

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    if (!name.trim() || nameError) return;
    setBusy(true);

    try {
      const b = await api.createBucket(name.trim(), admin && isPublic ? { mode: "public" } : undefined);
      if (!admin) {
        onCreated(b);
        return;
      }
      if (versioning) await api.setVersioning(b.name, "Enabled");
      // If quota was set, apply it
      if (quota) {
        const factor = quotaUnit === "TiB" ? 1024 * 1024 * 1024 * 1024 : 1024 * 1024 * 1024;
        const totalQuota = parseFloat(quotaAmount) * factor;
        if (!isNaN(totalQuota) && totalQuota > 0) {
          await api.setQuota(b.name, totalQuota);
        }
      }
      onCreated(b);
    } catch (err) {
      toast("error", errorText(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal
      open={open}
      onClose={onClose}
      size="xl"
      title={
        <span className="flex items-center gap-2">
          <Archive className="size-5 text-accent" />
          Create Bucket
        </span>
      }
    >
      <div className="grid grid-cols-1 gap-8 py-1 lg:grid-cols-3">
        {/* Left column: Form */}
        <form onSubmit={handleSubmit} className="space-y-6 lg:col-span-2">
          {/* Bucket Name input with naming-rule validation */}
          <div>
            <label htmlFor="bucket-name" className="mb-1.5 block text-xs font-semibold text-ink">
              Bucket Name*
            </label>
            <Input
              id="bucket-name"
              autoFocus
              value={name}
              onChange={(e) => setName(e.target.value.toLowerCase().trim())}
              placeholder=""
              className={cx(
                "h-9 font-mono",
                name && nameError && "border-danger focus:border-danger focus:ring-danger/20",
              )}
            />
            {name && nameError && (
              <span className="mt-1 block text-xs text-danger">{nameError}</span>
            )}
            <button
              type="button"
              onClick={() => setShowRules((r) => !r)}
              className="mt-2 flex items-center gap-1 text-xs text-muted hover:text-ink transition-colors"
            >
              <span>View Bucket Naming Rules</span>
              {showRules ? <ChevronUp className="size-3" /> : <ChevronDown className="size-3" />}
            </button>
            {showRules && (
              <ul className="mt-2 space-y-1 rounded-md bg-sunken p-3 text-xs text-muted">
                {NAME_RULES.map((r) => (
                  <li key={r}>• {r}</li>
                ))}
              </ul>
            )}
          </div>

          {!admin && (
            <p className="rounded-md bg-sunken p-3 text-xs text-muted">
              The bucket will be private and yours: you can read and write it. An administrator sets its access rules,
              versioning and quota.
            </p>
          )}

          {admin && (
          <>
          {/* Access Section */}
          <div className="space-y-3 border-t border-line pt-4">
            <div className="text-sm font-bold text-ink">Access</div>
            <div className="grid gap-2 sm:grid-cols-2" role="radiogroup" aria-label="Anonymous access">
              {(
                [
                  { value: false, title: "Private", body: "Only signed requests from access keys.", icon: Lock },
                  { value: true, title: "Public read", body: "Anyone can download objects by URL and list the bucket.", icon: Globe },
                ] as const
              ).map((o) => (
                <button
                  key={o.title}
                  type="button"
                  role="radio"
                  aria-checked={isPublic === o.value}
                  onClick={() => setIsPublic(o.value)}
                  className={cx(
                    "rounded-md border p-3 text-left transition-colors",
                    isPublic === o.value ? "border-accent bg-accent-soft/60 ring-1 ring-accent" : "border-line-strong hover:bg-sunken",
                  )}
                >
                  <o.icon className={cx("mb-1.5 size-4", isPublic === o.value ? "text-accent" : "text-faint")} />
                  <div className="text-xs font-semibold">{o.title}</div>
                  <div className="mt-0.5 text-[11px] text-muted">{o.body}</div>
                </button>
              ))}
            </div>
            {isPublic && (
              <p className="text-xs text-warn">
                Every object will be readable without credentials. To open only some folders or files, keep it private and
                use Make public in the Object Browser.
              </p>
            )}
          </div>

          {/* Features Section */}
          <div className="space-y-4 border-t border-line pt-4">
            <div className="text-sm font-bold text-ink">Features</div>

            {/* Versioning Toggle */}
            <div className="flex items-center justify-between">
              <span className="text-xs font-semibold text-ink">Versioning</span>
              <label className="flex cursor-pointer items-center gap-2 select-none text-xs">
                <span className={cx(!versioning ? "font-bold text-ink" : "text-muted")}>OFF</span>
                <div
                  onClick={() => setVersioning((v) => !v)}
                  className={cx(
                    "relative flex h-5 w-9 shrink-0 items-center rounded-full transition-colors",
                    versioning ? "bg-accent" : "bg-line-strong",
                  )}
                >
                  <span
                    className={cx(
                      "absolute size-3.5 rounded-full bg-white shadow transition-transform",
                      versioning ? "translate-x-4" : "translate-x-0.5",
                    )}
                  />
                </div>
                <span className={cx(versioning ? "font-bold text-ink" : "text-muted")}>ON</span>
              </label>
            </div>

            {/* Quota Toggle */}
            <div className="space-y-2">
              <div className="flex items-center justify-between">
                <span className="text-xs font-semibold text-ink">Quota</span>
                <label className="flex cursor-pointer items-center gap-2 select-none text-xs">
                  <span className={cx(!quota ? "font-bold text-ink" : "text-muted")}>OFF</span>
                  <div
                    onClick={() => setQuota((q) => !q)}
                    className={cx(
                      "relative flex h-5 w-9 shrink-0 items-center rounded-full transition-colors",
                      quota ? "bg-accent" : "bg-line-strong",
                    )}
                  >
                    <span
                      className={cx(
                        "absolute size-3.5 rounded-full bg-white shadow transition-transform",
                        quota ? "translate-x-4" : "translate-x-0.5",
                      )}
                    />
                  </div>
                  <span className={cx(quota ? "font-bold text-ink" : "text-muted")}>ON</span>
                </label>
              </div>
              {quota && (
                <div className="flex items-center gap-2 pt-1">
                  <Input
                    type="number"
                    min="1"
                    value={quotaAmount}
                    onChange={(e) => setQuotaAmount(e.target.value)}
                    className="w-24 h-8 text-xs font-mono"
                  />
                  <select
                    name="quota-unit"
                    aria-label="Quota unit"
                    value={quotaUnit}
                    onChange={(e) => setQuotaUnit(e.target.value as "GiB" | "TiB")}
                    className="h-8 rounded-md border border-line-strong bg-surface px-2 text-xs text-ink"
                  >
                    <option value="GiB">GiB</option>
                    <option value="TiB">TiB</option>
                  </select>
                </div>
              )}
            </div>

            {/* Object locking and retention are not offered: bin does not enforce them yet. */}
          </div>
          </>
          )}

          {/* Action buttons */}
          <div className="flex justify-end gap-3 pt-3">
            <Button type="button" onClick={handleClear}>
              Clear
            </Button>
            <Button
              type="submit"
              variant="primary"
              busy={busy}
              disabled={!name.trim() || !!nameError}
            >
              Create Bucket
            </Button>
          </div>
        </form>

        {/* Right column: explanatory sidebar */}
        <div className="space-y-4 rounded-xl border border-line bg-surface p-5 text-xs text-muted shadow-sm">
          <div className="flex items-center gap-2 font-bold text-ink text-sm">
            <Archive className="size-4 text-accent" />
            <span>Buckets</span>
          </div>
          <p className="leading-relaxed">
            bin uses buckets to organize objects. A bucket is similar to a folder or directory in a
            filesystem, where each bucket can hold an arbitrary number of objects.
          </p>
          <div className="space-y-3 pt-2">
            <div>
              <strong className="block text-ink font-semibold">Versioning</strong>
              <span>allows to keep multiple versions of the same object under the same key.</span>
            </div>
            <div>
              <strong className="block text-ink font-semibold">Quota</strong>
              <span>limits the amount of data in the bucket.</span>
            </div>
          </div>
        </div>
      </div>
    </Modal>
  );
}

export function BucketsPage() {
  const [buckets, setBuckets] = useState<Bucket[] | null>(null);
  const [filter, setFilter] = useState("");
  const [creating, setCreating] = useState(false);
  const [loading, setLoading] = useState(false);
  const toast = useToast();
  const navigate = useNavigate();
  const { session } = useAuth();
  const isAdmin = useIsAdmin();
  const canCreate = !!session?.canCreateBuckets;
  const [deleting, setDeleting] = useState<Bucket | null>(null);
  const [deleteBusy, setDeleteBusy] = useState(false);

  async function deleteOwn() {
    if (!deleting) return;
    setDeleteBusy(true);
    try {
      await api.deleteBucket(deleting.name, false);
      toast("ok", `Bucket ${deleting.name} deleted.`);
      setDeleting(null);
      load();
    } catch (e) {
      toast("error", errorText(e));
    } finally {
      setDeleteBusy(false);
    }
  }

  const load = () => {
    setLoading(true);
    api
      .buckets()
      .then(setBuckets)
      .catch((e) => toast("error", errorText(e)))
      .finally(() => setLoading(false));
  };

  useEffect(() => {
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const shown = useMemo(
    () => (buckets ?? []).filter((b) => b.name.toLowerCase().includes(filter.toLowerCase())),
    [buckets, filter],
  );

  return (
    <div className="space-y-5">
      {/* Header */}
      <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
        <h1 className="text-2xl font-bold tracking-tight text-ink">Object Browser</h1>
        {canCreate && (
          <Button variant="primary" icon={<Plus className="size-4" />} onClick={() => setCreating(true)}>
            Create Bucket
          </Button>
        )}
      </div>

      {/* Filter and Refresh Bar */}
      <div className="flex items-center justify-between gap-3">
        <div className="relative flex-1 max-w-md">
          <Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-faint" />
          <Input
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            placeholder="Filter Buckets"
            className="pl-9 h-9"
          />
        </div>
        <IconButton label="Refresh" onClick={load}>
          <RefreshCw className={cx("size-4", loading && "animate-spin")} />
        </IconButton>
      </div>

      {/* Buckets Table */}
      <div className="overflow-hidden rounded-card border border-line bg-surface shadow-sm">
        {buckets === null ? (
          <div className="flex justify-center py-20">
            <Spinner />
          </div>
        ) : shown.length === 0 ? (
          <div className="py-12 text-center text-sm text-faint">
            {filter
              ? `No bucket matches "${filter}".`
              : isAdmin
                ? "No buckets yet."
                : "Your account has no buckets yet. Ask an administrator for access" + (canCreate ? ", or create one." : ".")}
          </div>
        ) : (
          <table className="w-full text-sm">
            <thead className="border-b border-line bg-sunken/60 text-left">
              <tr className="label-strip">
                <th className="py-2.5 pl-4 pr-3 font-semibold text-ink">Name</th>
                <th className="py-2.5 pr-3 font-semibold text-ink">Objects</th>
                <th className="py-2.5 pr-3 font-semibold text-ink">Size</th>
                <th className="py-2.5 pr-4 font-semibold text-ink">Access</th>
                {isAdmin && <th className="hidden py-2.5 pr-4 font-semibold text-ink md:table-cell">Created by</th>}
                <th className="py-2.5 pr-4 text-right font-semibold text-ink">Actions</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-line">
              {shown.map((b) => (
                <tr
                  key={b.name}
                  onClick={() => navigate(`/buckets/${encodeURIComponent(b.name)}`)}
                  className="group cursor-pointer hover:bg-sunken/60 transition-colors"
                >
                  <td className="py-3 pl-4 pr-3">
                    <div className="flex items-center gap-2.5">
                      <Archive className="size-4 shrink-0 text-accent group-hover:scale-105 transition-transform" />
                      <span className="font-mono font-medium text-ink group-hover:text-accent">
                        {b.name}
                      </span>
                    </div>
                  </td>
                  <td className="py-3 pr-3 font-mono text-xs text-muted">{count(b.objects)}</td>
                  <td className="py-3 pr-3 font-mono text-xs text-muted">{bytes(b.size)}</td>
                  <td className="py-3 pr-4 font-mono text-xs font-semibold text-ink">
                    {/* Admins see the bucket's anonymous access; users what they may do with it. */}
                    {isAdmin ? bucketAccessLabel(b) : b.access === "write" ? "READ & WRITE" : "READ ONLY"}
                  </td>
                  {isAdmin && (
                    <td className="hidden py-3 pr-4 font-mono text-xs text-muted md:table-cell" title="Who created the bucket">
                      {b.createdBy || "—"}
                    </td>
                  )}
                  <td className="py-3 pr-4 text-right">
                    <div className="flex items-center justify-end gap-1" onClick={(e) => e.stopPropagation()}>
                      <button
                        type="button"
                        onClick={() => navigate(`/connect?bucket=${encodeURIComponent(b.name)}`)}
                        title="Connect SDK code generator"
                        className="flex size-7 items-center justify-center rounded text-muted hover:bg-accent/15 hover:text-accent transition-colors"
                      >
                        <Code2 className="size-3.5" />
                      </button>
                      {!isAdmin && b.owned && (
                        <button
                          type="button"
                          onClick={() => setDeleting(b)}
                          title="Delete this bucket (it must be empty)"
                          className="flex size-7 items-center justify-center rounded text-muted hover:bg-danger-soft hover:text-danger transition-colors"
                        >
                          <Trash2 className="size-3.5" />
                        </button>
                      )}
                      {isAdmin && (
                        <button
                          type="button"
                          onClick={() => navigate(`/buckets/${encodeURIComponent(b.name)}/settings`)}
                          title="Bucket settings"
                          className="flex size-7 items-center justify-center rounded text-muted hover:bg-sunken hover:text-ink transition-colors"
                        >
                          <Settings2 className="size-3.5" />
                        </button>
                      )}
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      {/* Users delete their own buckets, once empty (the server refuses otherwise). */}
      <Modal
        open={!!deleting}
        onClose={() => !deleteBusy && setDeleting(null)}
        title="Delete bucket"
        footer={
          <>
            <Button disabled={deleteBusy} onClick={() => setDeleting(null)}>
              Cancel
            </Button>
            <Button variant="danger" icon={<Trash2 className="size-4" />} busy={deleteBusy} onClick={deleteOwn}>
              Delete
            </Button>
          </>
        }
      >
        <p className="text-sm">
          Delete <span className="font-mono">{deleting?.name}</span>? It must be empty, its trash too.
        </p>
      </Modal>

      <CreateBucketModal
        admin={isAdmin}
        open={creating}
        onClose={() => setCreating(false)}
        onCreated={(b) => {
          setCreating(false);
          toast("ok", `Bucket ${b.name} created.`);
          navigate(`/buckets/${encodeURIComponent(b.name)}`);
        }}
      />
    </div>
  );
}
