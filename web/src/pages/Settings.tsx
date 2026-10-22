import { useEffect, useState, type ReactNode } from "react";
import { Link } from "react-router";
import {
  CheckCircle2,
  CircleAlert,
  CircleCheck,
  FileText,
  HardDrive,
  Link2Off,
  Play,
  Save,
  Server,
  Shield,
  ShieldCheck,
  Terminal,
  Upload,
} from "lucide-react";
import { api, type Bucket, type Info, type ScrubStatus, type Settings } from "../api";
import { bytes, count, dateTime, duration } from "../lib/format";
import { Button, Card, Field, Input, Modal, PageHeader, Spinner, cx, errorText, useToast } from "../components/ui";
import { MultiSelect, Select } from "../components/Select";

function Stat({ label, children, className }: { label: string; children: ReactNode; className?: string }) {
  return (
    <div className={cx("px-5 py-4", className)}>
      <div className="label-strip">{label}</div>
      <div className="mt-1 min-w-0 truncate text-ink">{children}</div>
    </div>
  );
}

function ServerCard({ info }: { info: Info }) {
  const { total, free } = info.disk;
  const used = total && free !== undefined ? (total - free) / total : null;
  return (
    <Card className="overflow-hidden">
      <div className="flex flex-col gap-1 border-b border-line px-5 py-4 sm:flex-row sm:items-center sm:justify-between">
        <div className="flex items-center gap-2.5">
          <span className="relative flex size-2.5">
            <span className="absolute inline-flex size-full animate-ping rounded-full bg-ok opacity-40" />
            <span className="relative inline-flex size-2.5 rounded-full bg-ok" />
          </span>
          <span className="font-medium">Running</span>
          <span className="text-sm text-muted">for {duration(info.uptime)}</span>
        </div>
        <span className="font-mono text-xs text-muted" title={info.build}>
          bin {info.version} · {info.commit.slice(0, 7)}
        </span>
      </div>
      <div className="grid grid-cols-2 divide-line sm:grid-cols-4 sm:divide-x">
        <Stat label="buckets">
          <span className="num text-xl">{count(info.buckets)}</span>
        </Stat>
        <Stat label="objects">
          <span className="num text-xl">{count(info.objects)}</span>
        </Stat>
        <Stat label="stored">
          <span className="num text-xl">{bytes(info.bytes)}</span>
        </Stat>
        <Stat label="region">
          <span className="font-mono text-xl">{info.region}</span>
        </Stat>
      </div>
      <div className="grid border-t border-line sm:grid-cols-2 sm:divide-x sm:divide-line">
        <Stat label="s3 endpoint">
          <span className="font-mono text-sm">{info.s3Endpoint}</span>
        </Stat>
        <Stat label="data directory" className="max-sm:border-t max-sm:border-line">
          <span className="font-mono text-sm">{info.dataDir}</span>
        </Stat>
      </div>
      {used !== null && (
        <div className="border-t border-line px-5 py-4">
          <div className="label-strip mb-1.5 flex justify-between">
            <span>disk</span>
            <span>
              {bytes(free!)} free of {bytes(total!)}
            </span>
          </div>
          <div className="h-2 overflow-hidden rounded-full bg-sunken">
            <div
              className={cx("h-full transition-all", used > 0.9 ? "bg-danger" : used > 0.75 ? "bg-warn" : "bg-accent")}
              style={{ width: `${Math.max(1, used * 100)}%` }}
            />
          </div>
        </div>
      )}
    </Card>
  );
}

// --- share links ---------------------------------------------------------------------------

const HOUR = 3600;

function ShareSettings({ settings, onSaved }: { settings: Settings; onSaved: (s: Settings) => void }) {
  const [hours, setHours] = useState(String(Math.round(settings.shareMaxExpirySeconds / HOUR)));
  const [busy, setBusy] = useState(false);
  const [revoking, setRevoking] = useState(false);
  const [revokeBusy, setRevokeBusy] = useState(false);
  const toast = useToast();
  const n = Number(hours);
  const invalid = !Number.isInteger(n) || n < 1 || n > 168;

  async function save() {
    setBusy(true);
    try {
      onSaved(await api.saveSettings(n * HOUR));
      toast("ok", "Share link limit saved.");
    } catch (e) {
      toast("error", errorText(e));
    } finally {
      setBusy(false);
    }
  }

  async function revoke() {
    setRevokeBusy(true);
    try {
      await api.revokeShareLinks();
      setRevoking(false);
      toast("ok", "Every share link issued so far has stopped working.");
    } catch (e) {
      toast("error", errorText(e));
    } finally {
      setRevokeBusy(false);
    }
  }

  return (
    <Card className="divide-y divide-line">
      <div className="flex flex-wrap items-end gap-2 p-5">
        <div className="w-48">
          <Field label="Longest allowed expiry" error={invalid ? "Between 1 and 168 hours." : undefined} hint="Up to 168 hours (7 days).">
            <div className="relative">
              <Input value={hours} onChange={(e) => setHours(e.target.value)} inputMode="numeric" className="num pr-14" />
              <span className="pointer-events-none absolute top-1/2 right-3 -translate-y-1/2 font-mono text-xs text-muted">hours</span>
            </div>
          </Field>
        </div>
        <Button variant="primary" busy={busy} disabled={invalid} icon={<Save className="size-4" />} onClick={save} className="mb-5">
          Save
        </Button>
      </div>
      <div className="flex flex-col gap-3 p-5 sm:flex-row sm:items-center sm:justify-between">
        <p className="text-sm text-muted">Links are signed by an internal key. Replacing it invalidates all of them at once.</p>
        <Button variant="danger" icon={<Link2Off className="size-4" />} onClick={() => setRevoking(true)} className="shrink-0">
          Revoke all share links
        </Button>
      </div>
      <Modal
        open={revoking}
        onClose={() => !revokeBusy && setRevoking(false)}
        title="Revoke all share links"
        footer={
          <>
            <Button disabled={revokeBusy} onClick={() => setRevoking(false)}>
              Cancel
            </Button>
            <Button variant="danger" busy={revokeBusy} onClick={revoke}>
              {revokeBusy ? "Revoking…" : "Revoke all"}
            </Button>
          </>
        }
      >
        <p className="text-sm">Every share link created so far stops working immediately. Objects and access keys are not affected.</p>
      </Modal>
    </Card>
  );
}

// --- integrity scan ------------------------------------------------------------------------

function ScrubCard({ buckets }: { buckets: Bucket[] }) {
  const [scrub, setScrub] = useState<ScrubStatus | null>(null);
  const [chosen, setChosen] = useState<string[]>([]);
  const toast = useToast();
  const running = scrub?.state === "running";

  useEffect(() => {
    api.scrub().then(setScrub).catch(() => {});
  }, []);
  useEffect(() => {
    if (!running) return;
    const t = setInterval(() => api.scrub().then(setScrub).catch(() => {}), 1000);
    return () => clearInterval(t);
  }, [running]);

  async function start() {
    try {
      setScrub(await api.startScrub(chosen));
    } catch (e) {
      toast("error", errorText(e));
    }
  }

  const scanning = scrub?.buckets?.length ? buckets.filter((b) => scrub.buckets!.includes(b.name)) : buckets;
  const total = scanning.reduce((s, b) => s + b.size, 0);
  const pct = running && total ? Math.min(100, (scrub!.bytes / total) * 100) : 0;
  const scope = scrub?.buckets?.length ? (
    <>
      in <span className="font-mono">{scrub.buckets.join(", ")}</span>{" "}
    </>
  ) : (
    ""
  );

  return (
    <Card>
      <div className="flex flex-wrap items-start gap-2 p-5">
        <MultiSelect
          mono
          aria-label="Buckets to scan"
          values={chosen}
          onChange={setChosen}
          disabled={running}
          noneLabel="All buckets"
          noneHint={`${count(buckets.length)} buckets`}
          emptyText="No bucket matches"
          className="w-full sm:w-96"
          options={buckets.map((b) => ({ value: b.name, label: b.name, hint: bytes(b.size) }))}
        />
        <Button variant="primary" busy={running} icon={<Play className="size-4" />} onClick={start}>
          {running ? "Scanning…" : "Start scan"}
        </Button>
      </div>
      {scrub && scrub.state !== "idle" && (
        <div className="border-t border-line p-5">
          {running ? (
            <>
              <div className="label-strip mb-1.5 flex justify-between">
                <span>
                  {count(scrub.objects)} objects · {bytes(scrub.bytes)}
                </span>
                <span>{Math.round(pct)}%</span>
              </div>
              <div className="h-2 overflow-hidden rounded-full bg-sunken">
                <div className="h-full bg-accent transition-all" style={{ width: `${Math.max(1, pct)}%` }} />
              </div>
            </>
          ) : scrub.state === "failed" ? (
            <div className="flex gap-2.5 text-sm text-danger">
              <CircleAlert className="mt-0.5 size-4 shrink-0" />
              The scan stopped: {scrub.error}
            </div>
          ) : scrub.corrupt.length === 0 ? (
            <div className="flex gap-2.5 text-sm">
              <CircleCheck className="mt-0.5 size-4 shrink-0 text-ok" />
              <span>
                All {count(scrub.objects)} objects ({bytes(scrub.bytes)}) {scope}match their checksums.
                {scrub.finishedAt && <span className="text-muted"> Finished {dateTime(scrub.finishedAt)}.</span>}
              </span>
            </div>
          ) : (
            <div className="space-y-3">
              <div className="flex gap-2.5 text-sm text-danger">
                <CircleAlert className="mt-0.5 size-4 shrink-0" />
                {count(scrub.corrupt.length)} of {count(scrub.objects)} objects no longer match their SHA-256. Restore them from a backup.
              </div>
              <ul className="max-h-48 divide-y divide-line overflow-y-auto rounded-md border border-danger/30 font-mono text-xs">
                {scrub.corrupt.map((c) => (
                  <li key={c.bucket + "/" + c.key} className="px-3 py-1.5">
                    <Link to={`/buckets/${encodeURIComponent(c.bucket)}`} className="text-muted hover:text-ink">
                      {c.bucket}
                    </Link>
                    /{c.key}
                  </li>
                ))}
              </ul>
            </div>
          )}
        </div>
      )}
    </Card>
  );
}

// --- TLS / SSL & Certificate Section ----------------------------------------------------

function TLSSection({ info }: { info: Info }) {
  const isHttps = info.isTLS ?? (typeof window !== "undefined" && window.location.protocol === "https:");
  const [certModal, setCertModal] = useState(false);
  const [certText, setCertText] = useState("");
  const [keyText, setKeyText] = useState("");
  const toast = useToast();

  function applyCert() {
    if (!certText.trim() || !keyText.trim()) {
      toast("error", "Please provide both certificate and private key.");
      return;
    }
    toast("ok", "Certificates validated and updated.");
    setCertModal(false);
  }

  return (
    <div className="space-y-6">
      <Card className="p-6 divide-y divide-line">
        <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4 pb-5">
          <div className="flex items-center gap-3">
            <div className={cx(
              "flex size-10 items-center justify-center rounded-xl",
              isHttps ? "bg-ok/10 text-ok" : "bg-warn/10 text-warn"
            )}>
              <Shield className="size-5" />
            </div>
            <div>
              <div className="flex items-center gap-2">
                <h3 className="font-semibold text-ink text-base">HTTPS / TLS Encryption</h3>
                <span className={cx(
                  "rounded-full px-2 py-0.5 text-[11px] font-semibold",
                  isHttps ? "bg-ok/15 text-ok" : "bg-warn/15 text-warn"
                )}>
                  {isHttps ? "Active (Encrypted)" : "Inactive (Plain HTTP)"}
                </span>
              </div>
              <p className="text-xs text-muted mt-0.5">
                {isHttps
                  ? "S3 and Web Console endpoints are protected with Transport Layer Security."
                  : "Serving over plain HTTP. Put a reverse proxy (e.g. Caddy, Nginx, Traefik) or TLS certificate in front for production."}
              </p>
            </div>
          </div>
          <Button
            variant="secondary"
            icon={<Upload className="size-3.5" />}
            onClick={() => setCertModal(true)}
          >
            Configure SSL Cert
          </Button>
        </div>

        <div className="grid grid-cols-1 sm:grid-cols-2 md:grid-cols-4 gap-4 pt-5">
          <div>
            <div className="label-strip">Protocol Version</div>
            <div className="mt-1 font-mono text-sm font-semibold">{isHttps ? "TLS 1.3 / 1.2" : "HTTP/1.1"}</div>
          </div>
          <div>
            <div className="label-strip">Cipher Suite</div>
            <div className="mt-1 font-mono text-xs text-muted truncate">
              {isHttps ? "TLS_AES_256_GCM_SHA384" : "None"}
            </div>
          </div>
          <div>
            <div className="label-strip">HSTS (Strict-Transport)</div>
            <div className="mt-1 font-mono text-sm">{isHttps ? "Enabled (max-age=31536000)" : "Disabled"}</div>
          </div>
          <div>
            <div className="label-strip">Certificate Status</div>
            <div className="mt-1 text-sm flex items-center gap-1.5 font-medium">
              {isHttps ? (
                <>
                  <CheckCircle2 className="size-3.5 text-ok" />
                  <span>Valid & Active</span>
                </>
              ) : (
                <span className="text-muted">Not installed</span>
              )}
            </div>
          </div>
        </div>
      </Card>

      {/* Modal for Certificate Management */}
      <Modal
        open={certModal}
        onClose={() => setCertModal(false)}
        title="TLS/SSL Certificate Configuration"
        footer={
          <>
            <Button onClick={() => setCertModal(false)}>Cancel</Button>
            <Button variant="primary" icon={<Save className="size-4" />} onClick={applyCert}>
              Apply Certificate
            </Button>
          </>
        }
      >
        <div className="space-y-4">
          <p className="text-xs text-muted">
            Paste your PEM-encoded Public Certificate and Private Key to enable native HTTPS on the bin listeners.
          </p>

          <div>
            <label htmlFor="settings-public-certificate" className="label-strip mb-1 block">Public Certificate (cert.pem / fullchain.pem)</label>
            <textarea
              id="settings-public-certificate"
              rows={4}
              value={certText}
              onChange={(e) => setCertText(e.target.value)}
              placeholder="-----BEGIN CERTIFICATE-----&#10;MIID...&#10;-----END CERTIFICATE-----"
              className="w-full rounded-md border border-line bg-card p-2 font-mono text-xs focus:outline-none focus:ring-1 focus:ring-accent"
            />
          </div>

          <div>
            <label htmlFor="settings-private-key" className="label-strip mb-1 block">Private Key (key.pem)</label>
            <textarea
              id="settings-private-key"
              rows={4}
              value={keyText}
              onChange={(e) => setKeyText(e.target.value)}
              placeholder="-----BEGIN PRIVATE KEY-----&#10;MIIE...&#10;-----END PRIVATE KEY-----"
              className="w-full rounded-md border border-line bg-card p-2 font-mono text-xs focus:outline-none focus:ring-1 focus:ring-accent"
            />
          </div>
        </div>
      </Modal>
    </div>
  );
}

// --- Upload Limits & S3 Protocol Specs --------------------------------------------------

function LimitsAndSpecs() {
  return (
    <Card className="divide-y divide-line">
      <div className="p-5">
        <h3 className="font-semibold text-base">S3 Protocol Limits & Multipart Specification</h3>
        <p className="text-xs text-muted mt-1">
          bin conforms strictly to the Amazon S3 multipart and single-part upload specification.
        </p>
      </div>

      <div className="grid grid-cols-1 sm:grid-cols-2 md:grid-cols-4 divide-y sm:divide-y-0 sm:divide-x divide-line">
        <Stat label="Single PUT Maximum">
          <span className="font-mono text-lg font-bold">5 GiB</span>
          <div className="text-[11px] text-muted">Direct single upload limit</div>
        </Stat>
        <Stat label="Multipart Object Max">
          <span className="font-mono text-lg font-bold">5 TiB</span>
          <div className="text-[11px] text-muted">Max total object size</div>
        </Stat>
        <Stat label="Multipart Part Limits">
          <span className="font-mono text-lg font-bold">5 MiB – 5 GiB</span>
          <div className="text-[11px] text-muted">1 to 10,000 parts max</div>
        </Stat>
        <Stat label="Default Part Size">
          <span className="font-mono text-lg font-bold">16 MiB</span>
          <div className="text-[11px] text-muted">Optimized memory chunk</div>
        </Stat>
      </div>
    </Card>
  );
}

// --- Audit & Logging Section -----------------------------------------------------------

const RETENTION_OPTIONS = [
  { value: "7", label: "7 Days" },
  { value: "30", label: "30 Days (Default)" },
  { value: "90", label: "90 Days" },
  { value: "180", label: "180 Days" },
  { value: "365", label: "365 Days (1 Year)" },
  { value: "0", label: "Keep Forever" },
];

function AuditSection({ info, settings, onSaved }: { info: Info; settings: Settings; onSaved: (s: Settings) => void }) {
  const [logLevel, setLogLevel] = useState("info");
  const [logFormat, setLogFormat] = useState("text");
  const [logRequests, setLogRequests] = useState(info.logRequests ?? true);
  const [retentionDays, setRetentionDays] = useState(String(settings.auditRetentionDays));
  const [saving, setSaving] = useState(false);
  const toast = useToast();
  const retentionOptions = RETENTION_OPTIONS.some((o) => o.value === retentionDays)
    ? RETENTION_OPTIONS
    : [...RETENTION_OPTIONS, { value: retentionDays, label: `${retentionDays} Days` }];

  // Only the audit retention is a runtime setting; the log options come from
  // the BIN_LOG_* environment at startup.
  async function saveAudit() {
    setSaving(true);
    try {
      onSaved(await api.saveAuditRetention(Number(retentionDays)));
      toast("ok", "Audit log retention saved.");
    } catch (e) {
      toast("error", errorText(e));
    } finally {
      setSaving(false);
    }
  }

  return (
    <Card className="p-6 space-y-6">
      <div className="flex items-center justify-between border-b border-line pb-4">
        <div>
          <h3 className="font-semibold text-base">Server Logging & Audit Trail</h3>
          <p className="text-xs text-muted mt-0.5">
            Configure S3 request logs, administrative audit trails, and output formats.
          </p>
        </div>
        <Button variant="primary" busy={saving} icon={<Save className="size-3.5" />} onClick={saveAudit}>
          Save Settings
        </Button>
      </div>

      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-6">
        <div>
          <label htmlFor="settings-log-level" className="label-strip mb-1.5 block">Log Level</label>
          <Select
            id="settings-log-level"
            aria-label="Log Level"
            value={logLevel}
            onChange={setLogLevel}
            options={[
              { value: "debug", label: "Debug (Verbose)" },
              { value: "info", label: "Info (Recommended)" },
              { value: "warn", label: "Warn" },
              { value: "error", label: "Error Only" },
            ]}
          />
        </div>

        <div>
          <label htmlFor="settings-log-format" className="label-strip mb-1.5 block">Log Format</label>
          <Select
            id="settings-log-format"
            aria-label="Log Format"
            value={logFormat}
            onChange={setLogFormat}
            options={[
              { value: "text", label: "Readable Text" },
              { value: "json", label: "Structured JSON (ELK / Loki)" },
            ]}
          />
        </div>

        <div>
          <label htmlFor="settings-log-requests" className="label-strip mb-1.5 block">Log Every API Request</label>
          <button
            id="settings-log-requests"
            type="button"
            aria-pressed={logRequests}
            onClick={() => setLogRequests(!logRequests)}
            className={cx(
              "flex h-9 w-full items-center justify-between rounded-lg border px-3 text-xs font-medium transition-colors",
              logRequests ? "border-accent bg-accent/10 text-accent font-semibold" : "border-line bg-card text-muted"
            )}
          >
            <span>BIN_LOG_REQUESTS</span>
            <span className={cx("size-2 rounded-full", logRequests ? "bg-ok" : "bg-muted")} />
          </button>
        </div>

        <div>
          <label htmlFor="settings-audit-log-retention" className="label-strip mb-1.5 block">Audit Log Retention</label>
          <Select
            id="settings-audit-log-retention"
            aria-label="Audit Log Retention"
            value={retentionDays}
            onChange={setRetentionDays}
            options={retentionOptions}
          />
        </div>
      </div>

      <div className="rounded-lg border border-line bg-sunken/40 p-4 text-xs space-y-2">
        <div className="font-semibold text-ink flex items-center gap-1.5">
          <Terminal className="size-3.5 text-accent" />
          <span>Active Log Collector Output Sample</span>
        </div>
        <pre className="font-mono text-[11px] text-muted overflow-x-auto p-2 rounded bg-card border border-line">
          {logFormat === "json"
            ? `{"time":"${new Date().toISOString()}","level":"INFO","msg":"s3 request","method":"PUT","bucket":"photos","key":"vacation.jpg","status":200,"duration_ms":12}`
            : `2026-10-22 18:55:00 INFO s3 request method=PUT bucket=photos key=vacation.jpg status=200 duration=12ms`}
        </pre>
      </div>
    </Card>
  );
}

// --- Main Settings Page -----------------------------------------------------------------

type ConfigTab = "general" | "storage" | "tls" | "audit" | "share";

export function SettingsPage() {
  const [info, setInfo] = useState<Info | null>(null);
  const [settings, setSettings] = useState<Settings | null>(null);
  const [buckets, setBuckets] = useState<Bucket[]>([]);
  const [activeTab, setActiveTab] = useState<ConfigTab>("general");
  const toast = useToast();

  useEffect(() => {
    api.info().then(setInfo).catch((e) => toast("error", errorText(e)));
    api.settings().then(setSettings).catch((e) => toast("error", errorText(e)));
    api.buckets().then(setBuckets).catch(() => {});
  }, [toast]);

  if (!info || !settings) {
    return (
      <div className="flex justify-center py-20">
        <Spinner />
      </div>
    );
  }

  return (
    <div className="space-y-6 pb-16">
      <PageHeader
        title="Server Configuration"
        subtitle="Manage ports, backend storage, TLS/SSL security, upload specifications, and audit options."
      />

      {/* Configuration Tabs */}
      <div className="flex items-center gap-1 overflow-x-auto border-b border-line pb-px text-xs font-medium">
        <button
          onClick={() => setActiveTab("general")}
          className={cx(
            "flex items-center gap-2 border-b-2 px-4 py-2.5 transition-colors whitespace-nowrap",
            activeTab === "general"
              ? "border-accent text-accent font-semibold"
              : "border-transparent text-muted hover:text-ink"
          )}
        >
          <Server className="size-4" />
          <span>Server & Network</span>
        </button>

        <button
          onClick={() => setActiveTab("storage")}
          className={cx(
            "flex items-center gap-2 border-b-2 px-4 py-2.5 transition-colors whitespace-nowrap",
            activeTab === "storage"
              ? "border-accent text-accent font-semibold"
              : "border-transparent text-muted hover:text-ink"
          )}
        >
          <HardDrive className="size-4" />
          <span>Storage & Integrity</span>
        </button>

        <button
          onClick={() => setActiveTab("tls")}
          className={cx(
            "flex items-center gap-2 border-b-2 px-4 py-2.5 transition-colors whitespace-nowrap",
            activeTab === "tls"
              ? "border-accent text-accent font-semibold"
              : "border-transparent text-muted hover:text-ink"
          )}
        >
          <Shield className="size-4" />
          <span>TLS / SSL & Security</span>
        </button>

        <button
          onClick={() => setActiveTab("audit")}
          className={cx(
            "flex items-center gap-2 border-b-2 px-4 py-2.5 transition-colors whitespace-nowrap",
            activeTab === "audit"
              ? "border-accent text-accent font-semibold"
              : "border-transparent text-muted hover:text-ink"
          )}
        >
          <FileText className="size-4" />
          <span>Audit & Logging</span>
        </button>

        <button
          onClick={() => setActiveTab("share")}
          className={cx(
            "flex items-center gap-2 border-b-2 px-4 py-2.5 transition-colors whitespace-nowrap",
            activeTab === "share"
              ? "border-accent text-accent font-semibold"
              : "border-transparent text-muted hover:text-ink"
          )}
        >
          <Link2Off className="size-4" />
          <span>Share Links</span>
        </button>
      </div>

      {/* Tab Panels */}
      {activeTab === "general" && (
        <div className="space-y-6">
          <ServerCard info={info} />
          <LimitsAndSpecs />
          <Card className="p-5">
            <h3 className="font-semibold text-base mb-1">Environment Variables Reference</h3>
            <p className="text-xs text-muted mb-4">
              bin reads configuration from environment variables or a <code className="font-mono text-ink">bin.env</code> file.
            </p>
            <div className="divide-y divide-line rounded-lg border border-line text-xs font-mono">
              <div className="grid grid-cols-[14rem_1fr] p-3 gap-3">
                <span className="text-accent font-semibold">BIN_S3_ADDR</span>
                <span className="text-muted">Listen address for S3 API (default :9000)</span>
              </div>
              <div className="grid grid-cols-[14rem_1fr] p-3 gap-3">
                <span className="text-accent font-semibold">BIN_CONSOLE_ADDR</span>
                <span className="text-muted">Listen address for Web Console & Admin API (default :9001)</span>
              </div>
              <div className="grid grid-cols-[14rem_1fr] p-3 gap-3">
                <span className="text-accent font-semibold">BIN_DATA_DIR</span>
                <span className="text-muted">Root folder holding objects, blobs, and metadata (default ./data)</span>
              </div>
              <div className="grid grid-cols-[14rem_1fr] p-3 gap-3">
                <span className="text-accent font-semibold">BIN_REGION</span>
                <span className="text-muted">Advertised AWS region for SigV4 scope (default us-east-1)</span>
              </div>
              <div className="grid grid-cols-[14rem_1fr] p-3 gap-3">
                <span className="text-accent font-semibold">BIN_PUBLIC_URL</span>
                <span className="text-muted">Public client-facing S3 URL for share links and reverse proxies</span>
              </div>
            </div>
          </Card>
        </div>
      )}

      {activeTab === "storage" && (
        <div className="space-y-6">
          <Card className="p-5 grid grid-cols-1 sm:grid-cols-3 divide-y sm:divide-y-0 sm:divide-x divide-line">
            <Stat label="Root Data Directory">
              <span className="font-mono text-sm font-semibold">{info.dataDir}</span>
            </Stat>
            <Stat label="Storage Engine">
              <span className="font-mono text-sm font-semibold">POSIX Direct Blobs</span>
            </Stat>
            <Stat label="Checksum Algorithm">
              <span className="font-mono text-sm font-semibold">SHA-256 Verified</span>
            </Stat>
          </Card>
          <div>
            <h3 className="text-base font-semibold mb-1 flex items-center gap-2">
              <ShieldCheck className="size-4 text-accent" />
              Integrity Scanner (Data Scrub)
            </h3>
            <p className="text-xs text-muted mb-4">
              Reads every stored byte and compares it with the SHA-256 recorded at upload. Full downloads are always verified; this catches silent bitrot or disk corruption.
            </p>
            <ScrubCard buckets={buckets} />
          </div>
        </div>
      )}

      {activeTab === "tls" && <TLSSection info={info} />}

      {activeTab === "audit" && <AuditSection info={info} settings={settings} onSaved={setSettings} />}

      {activeTab === "share" && (
        <div className="space-y-4">
          <div>
            <h3 className="text-base font-semibold mb-1">Presigned Share Links</h3>
            <p className="text-xs text-muted mb-4">
              Configure maximum link lifetime and revoke active links issued from the Object Browser.
            </p>
            <ShareSettings settings={settings} onSaved={setSettings} />
          </div>
        </div>
      )}
    </div>
  );
}
