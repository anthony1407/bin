import { useEffect, useState } from "react";
import { Link } from "react-router";
import { Archive, Download, Upload } from "lucide-react";
import { api, type AccessKey, type KeyActivity } from "../api";
import { bytes, count, dateTime, relative } from "../lib/format";
import { Badge, Modal, Spinner, errorText, useToast } from "../components/ui";

/** What one user created, uploaded and downloaded, for admins. */
export function UserActivityModal({ user, onClose }: { user: AccessKey | null; onClose: () => void }) {
  const [act, setAct] = useState<KeyActivity | null>(null);
  const toast = useToast();

  useEffect(() => {
    if (!user) return;
    setAct(null);
    api
      .keyActivity(user.accessKey)
      .then(setAct)
      .catch((e) => {
        toast("error", errorText(e));
        onClose();
      });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [user]);

  if (!user) return null;
  const covers = act && (act.retentionDays === 0 ? "everything recorded" : `the last ${act.retentionDays} days`);

  return (
    <Modal open={!!user} onClose={onClose} wide title={`Activity: ${user.name || user.accessKey}`}>
      {!act ? (
        <Spinner className="mx-auto my-8" />
      ) : (
        <div className="space-y-5">
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
            <Stat icon={<Upload className="size-4 text-accent" />} label="Uploads" value={count(act.uploads)} detail={bytes(act.uploadBytes)} />
            <Stat
              icon={<Download className="size-4 text-accent" />}
              label="Downloads"
              value={act.downloadsRecorded || act.downloads > 0 ? count(act.downloads) : "—"}
              detail={act.downloadsRecorded ? bytes(act.downloadBytes) : "not recorded (Configuration → Audit)"}
            />
            <Stat
              label="Last activity"
              value={act.lastActivity ? relative(act.lastActivity) : "none"}
              detail={act.lastActivity ? dateTime(act.lastActivity) : ""}
            />
          </div>

          <section className="space-y-2">
            <h3 className="text-sm font-semibold">Buckets created ({act.buckets.length})</h3>
            {act.buckets.length === 0 ? (
              <p className="text-xs text-muted">This user has not created buckets.</p>
            ) : (
              <div className="divide-y divide-line rounded-md border border-line text-xs">
                {act.buckets.map((b) => (
                  <div key={b.name} className="flex items-center justify-between gap-3 px-3 py-2">
                    <Link to={`/buckets/${encodeURIComponent(b.name)}`} className="flex min-w-0 items-center gap-2 font-mono hover:text-accent" onClick={onClose}>
                      <Archive className="size-3.5 shrink-0 text-accent" />
                      <span className="truncate">{b.name}</span>
                    </Link>
                    <span className="shrink-0 text-muted">
                      {count(b.objects)} object{b.objects === 1 ? "" : "s"} · {bytes(b.size)} · {dateTime(b.createdAt)}
                      {!b.owned && <span title="The user no longer owns it (the key was deleted and recreated)"> · not owned</span>}
                    </span>
                  </div>
                ))}
              </div>
            )}
          </section>

          <section className="space-y-2">
            <div className="flex items-center justify-between">
              <h3 className="text-sm font-semibold">Latest actions</h3>
              <Link
                to={`/monitoring/audit?actor=${encodeURIComponent(user.accessKey)}`}
                className="text-xs text-accent hover:underline"
                onClick={onClose}
              >
                See all in the audit log
              </Link>
            </div>
            {act.recent.length === 0 ? (
              <p className="text-xs text-muted">Nothing recorded.</p>
            ) : (
              <div className="max-h-72 divide-y divide-line overflow-y-auto rounded-md border border-line text-xs">
                {act.recent.map((e) => (
                  <div key={e.id} className="grid grid-cols-[7.5rem_1fr_auto] items-center gap-3 px-3 py-1.5">
                    <span className="text-muted" title={dateTime(e.at)}>
                      {relative(e.at)}
                    </span>
                    <span className="min-w-0 truncate font-mono" title={[e.bucket, e.resource].filter(Boolean).join("/")}>
                      <span className="text-ink">{e.action}</span>{" "}
                      <span className="text-muted">{[e.bucket, e.resource].filter(Boolean).join("/")}</span>
                    </span>
                    <span className="flex shrink-0 items-center gap-1.5">
                      {e.size ? <span className="font-mono text-muted">{bytes(e.size)}</span> : null}
                      <Badge tone={e.source === "s3" ? "neutral" : "accent"}>{e.source === "s3" ? "S3" : "console"}</Badge>
                      {e.status >= 400 && <Badge tone="danger">{e.status}</Badge>}
                    </span>
                  </div>
                ))}
              </div>
            )}
          </section>

          <p className="text-[11px] text-faint">
            Uploads, downloads and actions come from the audit log, which keeps {covers}. Buckets and their contents are always
            current.
          </p>
        </div>
      )}
    </Modal>
  );
}

function Stat({ icon, label, value, detail }: { icon?: React.ReactNode; label: string; value: string; detail?: string }) {
  return (
    <div className="rounded-md border border-line p-3">
      <div className="flex items-center gap-1.5 text-[11px] text-muted">
        {icon}
        {label}
      </div>
      <div className="mt-1 text-lg font-semibold text-ink">{value}</div>
      {detail && <div className="text-[11px] text-muted">{detail}</div>}
    </div>
  );
}
