import { useEffect, useState, type ReactNode } from "react";
import { Link } from "react-router";
import { CircleAlert, CircleCheck, Link2Off, Play, Save, ShieldCheck } from "lucide-react";
import { api, type Bucket, type Info, type ScrubStatus, type Settings } from "../api";
import { bytes, count, dateTime, duration } from "../lib/format";
import { Button, Card, Field, Input, Modal, PageHeader, Spinner, cx, errorText, useToast } from "../components/ui";
import { Select } from "../components/Select";

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
            <div className={cx("h-full", used > 0.9 ? "bg-danger" : used > 0.75 ? "bg-warn" : "bg-accent")} style={{ width: `${Math.max(1, used * 100)}%` }} />
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
  const [bucket, setBucket] = useState("");
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
      setScrub(await api.startScrub(bucket));
    } catch (e) {
      toast("error", errorText(e));
    }
  }

  const total = bucket ? buckets.find((b) => b.name === bucket)?.size : buckets.reduce((s, b) => s + b.size, 0);
  const pct = running && total ? Math.min(100, (scrub!.bytes / total) * 100) : 0;

  return (
    <Card>
      <div className="flex flex-wrap items-center gap-2 p-5">
        <Select
          searchable
          mono
          aria-label="Bucket to scan"
          value={bucket}
          onChange={setBucket}
          disabled={running}
          placeholder="Search buckets"
          emptyText="No bucket matches"
          className="w-full sm:w-72"
          options={[
            { value: "", label: "All buckets", mono: false, hint: `${count(buckets.length)} buckets` },
            ...buckets.map((b) => ({ value: b.name, label: b.name, hint: bytes(b.size) })),
          ]}
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
                All {count(scrub.objects)} objects ({bytes(scrub.bytes)}) {scrub.bucket ? <>in <span className="font-mono">{scrub.bucket}</span> </> : ""}match their
                checksums.
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

// --- page ----------------------------------------------------------------------------------------

function Section({ title, description, icon, children }: { title: string; description: string; icon?: ReactNode; children: ReactNode }) {
  return (
    <section className="mt-10">
      <h2 className="flex items-center gap-2 text-base font-semibold">
        {icon}
        {title}
      </h2>
      <p className="mt-1 mb-4 text-sm text-muted">{description}</p>
      {children}
    </section>
  );
}

export function SettingsPage() {
  const [info, setInfo] = useState<Info | null>(null);
  const [settings, setSettings] = useState<Settings | null>(null);
  const [buckets, setBuckets] = useState<Bucket[]>([]);
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
    <div className="max-w-4xl">
      <PageHeader title="Settings" subtitle="Server status and console-wide options. Everything else is configured with BIN_* environment variables." />
      <ServerCard info={info} />
      <Section title="Share links" description="Presigned download links created from the object browser.">
        <ShareSettings settings={settings} onSaved={setSettings} />
      </Section>
      <Section
        title="Integrity scan"
        icon={<ShieldCheck className="size-4 text-accent" />}
        description="Reads every stored byte and compares it with the SHA-256 recorded at upload. Full downloads are always verified; this also catches objects nobody has read lately."
      >
        <ScrubCard buckets={buckets} />
      </Section>
    </div>
  );
}
