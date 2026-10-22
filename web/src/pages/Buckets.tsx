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
} from "lucide-react";
import { api, type Bucket } from "../api";
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

function minioAccessLabel(b: Bucket): string {
  if (b.policy.mode === "public") return "R/W";
  if (b.policy.mode === "custom") return "CUSTOM";
  return "PRIVATE";
}

function CreateBucketModal({
  open,
  onClose,
  onCreated,
}: {
  open: boolean;
  onClose: () => void;
  onCreated: (b: Bucket) => void;
}) {
  const [name, setName] = useState("");
  const [showRules, setShowRules] = useState(false);
  const [versioning, setVersioning] = useState(false);
  const [locking, setLocking] = useState(false);
  const [quota, setQuota] = useState(false);
  const [quotaAmount, setQuotaAmount] = useState("1");
  const [quotaUnit, setQuotaUnit] = useState<"GiB" | "TiB">("TiB");
  const [retention, setRetention] = useState(false);
  const [retentionDays, setRetentionDays] = useState("180");
  const [busy, setBusy] = useState(false);
  const toast = useToast();

  const nameError = validateBucketName(name);

  useEffect(() => {
    if (open) {
      setName("");
      setVersioning(false);
      setLocking(false);
      setQuota(false);
      setRetention(false);
      setShowRules(false);
    }
  }, [open]);

  function handleClear() {
    setName("");
    setVersioning(false);
    setLocking(false);
    setQuota(false);
    setRetention(false);
  }

  async function handleSubmit(e: FormEvent) {
    e.preventDefault();
    if (!name.trim() || nameError) return;
    setBusy(true);

    try {
      const b = await api.createBucket(name.trim());
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
    <Modal open={open} onClose={onClose} title="" wide={true}>
      <div className="grid grid-cols-1 gap-8 lg:grid-cols-3 -mt-2">
        {/* Left column: Form */}
        <form onSubmit={handleSubmit} className="space-y-6 lg:col-span-2">
          <div className="flex items-center gap-2 text-lg font-bold text-ink">
            <Archive className="size-5 text-accent" />
            <span>Create Bucket</span>
          </div>

          {/* Bucket Name input with MinIO validation */}
          <div>
            <label className="mb-1.5 block text-xs font-semibold text-ink">
              Bucket Name*
            </label>
            <Input
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

            {/* Object Locking Toggle */}
            <div className="flex items-center justify-between">
              <span className="text-xs font-semibold text-ink">Object Locking</span>
              <label className="flex cursor-pointer items-center gap-2 select-none text-xs">
                <span className={cx(!locking ? "font-bold text-ink" : "text-muted")}>OFF</span>
                <div
                  onClick={() => {
                    const next = !locking;
                    setLocking(next);
                    if (next) setVersioning(true);
                  }}
                  className={cx(
                    "relative flex h-5 w-9 shrink-0 items-center rounded-full transition-colors",
                    locking ? "bg-accent" : "bg-line-strong",
                  )}
                >
                  <span
                    className={cx(
                      "absolute size-3.5 rounded-full bg-white shadow transition-transform",
                      locking ? "translate-x-4" : "translate-x-0.5",
                    )}
                  />
                </div>
                <span className={cx(locking ? "font-bold text-ink" : "text-muted")}>ON</span>
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

            {/* Retention Toggle */}
            <div className="space-y-2">
              <div className="flex items-center justify-between">
                <span className="text-xs font-semibold text-ink">Retention</span>
                <label className="flex cursor-pointer items-center gap-2 select-none text-xs">
                  <span className={cx(!retention ? "font-bold text-ink" : "text-muted")}>OFF</span>
                  <div
                    onClick={() => {
                      const next = !retention;
                      setRetention(next);
                      if (next) setVersioning(true);
                    }}
                    className={cx(
                      "relative flex h-5 w-9 shrink-0 items-center rounded-full transition-colors",
                      retention ? "bg-accent" : "bg-line-strong",
                    )}
                  >
                    <span
                      className={cx(
                        "absolute size-3.5 rounded-full bg-white shadow transition-transform",
                        retention ? "translate-x-4" : "translate-x-0.5",
                      )}
                    />
                  </div>
                  <span className={cx(retention ? "font-bold text-ink" : "text-muted")}>ON</span>
                </label>
              </div>
              {retention && (
                <div className="flex items-center gap-2 pt-1 text-xs">
                  <Input
                    type="number"
                    min="1"
                    value={retentionDays}
                    onChange={(e) => setRetentionDays(e.target.value)}
                    className="w-24 h-8 text-xs font-mono"
                  />
                  <span className="text-muted">days</span>
                </div>
              )}
            </div>
          </div>

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

        {/* Right column: Explanatory MinIO sidebar */}
        <div className="space-y-4 rounded-xl border border-line bg-surface p-5 text-xs text-muted shadow-sm">
          <div className="flex items-center gap-2 font-bold text-ink text-sm">
            <Archive className="size-4 text-accent" />
            <span>Buckets</span>
          </div>
          <p className="leading-relaxed">
            MinIO uses buckets to organize objects. A bucket is similar to a folder or directory in a
            filesystem, where each bucket can hold an arbitrary number of objects.
          </p>
          <div className="space-y-3 pt-2">
            <div>
              <strong className="block text-ink font-semibold">Versioning</strong>
              <span>allows to keep multiple versions of the same object under the same key.</span>
            </div>
            <div>
              <strong className="block text-ink font-semibold">Object Locking</strong>
              <span>
                prevents objects from being deleted. Required to support retention and legal hold.
                Can only be enabled at bucket creation.
              </span>
            </div>
            <div>
              <strong className="block text-ink font-semibold">Quota</strong>
              <span>limits the amount of data in the bucket.</span>
            </div>
            <div>
              <strong className="block text-ink font-semibold">Retention</strong>
              <span>
                imposes rules to prevent object deletion for a period of time. Versioning must be
                enabled in order to set bucket retention policies.
              </span>
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
      {/* MinIO style Header */}
      <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
        <h1 className="text-2xl font-bold tracking-tight text-ink">Object Browser</h1>
        <Button
          variant="primary"
          icon={<Plus className="size-4" />}
          onClick={() => setCreating(true)}
        >
          Create Bucket
        </Button>
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

      {/* MinIO style Buckets Table */}
      <div className="overflow-hidden rounded-card border border-line bg-surface shadow-sm">
        {buckets === null ? (
          <div className="flex justify-center py-20">
            <Spinner />
          </div>
        ) : shown.length === 0 ? (
          <div className="py-12 text-center text-sm text-faint">
            {filter ? `No bucket matches "${filter}".` : "No buckets yet."}
          </div>
        ) : (
          <table className="w-full text-sm">
            <thead className="border-b border-line bg-sunken/60 text-left">
              <tr className="label-strip">
                <th className="py-2.5 pl-4 pr-3 font-semibold text-ink">Name</th>
                <th className="py-2.5 pr-3 font-semibold text-ink">Objects</th>
                <th className="py-2.5 pr-3 font-semibold text-ink">Size</th>
                <th className="py-2.5 pr-4 font-semibold text-ink">Access</th>
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
                    {minioAccessLabel(b)}
                  </td>
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
                      <button
                        type="button"
                        onClick={() => navigate(`/buckets/${encodeURIComponent(b.name)}/settings`)}
                        title="Bucket settings"
                        className="flex size-7 items-center justify-center rounded text-muted hover:bg-sunken hover:text-ink transition-colors"
                      >
                        <Settings2 className="size-3.5" />
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      <CreateBucketModal
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
