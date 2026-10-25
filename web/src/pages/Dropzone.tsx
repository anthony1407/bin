import { useState, useEffect, useRef } from "react";
import {
  UploadCloud,
  Copy,
  Trash2,
  Share2,
  ExternalLink,
  FileText,
  Plus,
  Check,
  X,
  ChevronDown,
  ChevronRight,
  FolderOpen,
} from "lucide-react";
import { Link } from "react-router";
import { api, dropzoneUpload, type Bucket, type Dropzone, type DropzoneUpload, type PublicDropzone } from "../api";
import { PageHeader, Card, Button, Input, Modal, Spinner, cx, errorText, useToast } from "../components/ui";
import { Select } from "../components/Select";
import { bytes, dateTime } from "../lib/format";
import { describeUserAgent } from "../lib/useragent";

type QueuedFile = {
  id: number;
  file: File;
  status: "pending" | "uploading" | "done" | "error" | "canceled";
  /** Bytes sent so far, from the browser's upload progress. */
  sent: number;
  /** Bytes per second, smoothed. */
  speed: number;
  detail?: string;
};

/** "12 s", "3 min" for a remaining-time estimate. */
function remaining(seconds: number): string {
  if (!isFinite(seconds) || seconds <= 0) return "";
  if (seconds < 60) return `${Math.ceil(seconds)} s left`;
  if (seconds < 3600) return `${Math.ceil(seconds / 60)} min left`;
  return `${(seconds / 3600).toFixed(1)} h left`;
}

function ProgressBar({ value, tone = "accent" }: { value: number; tone?: "accent" | "ok" | "danger" }) {
  return (
    <div className="h-1.5 overflow-hidden rounded-full bg-sunken" role="progressbar" aria-valuenow={Math.round(value)} aria-valuemin={0} aria-valuemax={100}>
      <div
        className={cx("h-full transition-[width] duration-200", tone === "ok" ? "bg-ok" : tone === "danger" ? "bg-danger" : "bg-accent")}
        style={{ width: `${Math.min(100, Math.max(0, value))}%` }}
      />
    </div>
  );
}

// --- Public upload page --------------------------------------------------------------

/**
 * The page a dropzone link opens, for people without an account. The token
 * is in the URL fragment, which the browser never sends to the server.
 */
export function PublicUploadPage() {
  const token = window.location.hash.slice(1);
  const [info, setInfo] = useState<PublicDropzone | null>(null);
  const [error, setError] = useState("");
  const [dragOver, setDragOver] = useState(false);
  const [files, setFiles] = useState<QueuedFile[]>([]);
  const [isUploading, setIsUploading] = useState(false);
  const fileInputRef = useRef<HTMLInputElement>(null);
  // The queue as the upload loop sees it: files added meanwhile are picked up too.
  const queue = useRef<QueuedFile[]>([]);
  const nextId = useRef(1);
  const aborts = useRef(new Map<number, AbortController>());

  useEffect(() => {
    if (!token) {
      setError("This upload link is incomplete. Ask for the full link again.");
      return;
    }
    api
      .publicDropzone(token)
      .then(setInfo)
      .catch((e) => setError(errorText(e)));
  }, [token]);

  function setQueue(next: QueuedFile[]) {
    queue.current = next;
    setFiles(next);
  }

  function update(id: number, patch: Partial<QueuedFile>) {
    setQueue(queue.current.map((f) => (f.id === id ? { ...f, ...patch } : f)));
  }

  function addFiles(list: FileList | null) {
    if (!list || !info) return;
    const added = Array.from(list).map<QueuedFile>((file) => ({
      id: nextId.current++,
      file,
      sent: 0,
      speed: 0,
      ...(file.size > info.maxBytes
        ? { status: "error" as const, detail: `Larger than the ${bytes(info.maxBytes)} this link allows` }
        : { status: "pending" as const }),
    }));
    setQueue([...queue.current, ...added]);
  }

  async function uploadOne(item: QueuedFile) {
    const ctrl = new AbortController();
    aborts.current.set(item.id, ctrl);
    update(item.id, { status: "uploading", sent: 0, speed: 0 });
    let lastTime = performance.now();
    let lastSent = 0;
    let speed = 0;
    try {
      const stored = await dropzoneUpload(
        token,
        item.file,
        (sent) => {
          const now = performance.now();
          const dt = (now - lastTime) / 1000;
          if (dt >= 0.25) {
            const instant = (sent - lastSent) / dt;
            speed = speed === 0 ? instant : speed * 0.7 + instant * 0.3;
            lastTime = now;
            lastSent = sent;
          }
          update(item.id, { sent, speed });
        },
        ctrl.signal,
      );
      update(item.id, {
        status: "done",
        sent: item.file.size,
        detail: stored !== item.file.name ? `Saved as ${stored}` : undefined,
      });
    } catch (e) {
      if (ctrl.signal.aborted) update(item.id, { status: "canceled", detail: "Canceled" });
      else update(item.id, { status: "error", detail: errorText(e) });
    } finally {
      aborts.current.delete(item.id);
    }
  }

  async function uploadAll() {
    setIsUploading(true);
    // One file at a time, so each gets the full bandwidth and its progress is meaningful.
    for (;;) {
      const next = queue.current.find((f) => f.status === "pending");
      if (!next) break;
      await uploadOne(next);
    }
    setIsUploading(false);
  }

  function cancel(id: number) {
    const ctrl = aborts.current.get(id);
    if (ctrl) ctrl.abort();
    else update(id, { status: "canceled", detail: "Canceled" });
  }

  const pending = files.filter((f) => f.status === "pending").length;
  // The whole batch: everything queued or sent in this session, except refused files.
  const batch = files.filter((f) => f.status !== "error" && f.status !== "canceled");
  const batchTotal = batch.reduce((n, f) => n + f.file.size, 0);
  const batchSent = batch.reduce((n, f) => n + (f.status === "done" ? f.file.size : f.sent), 0);
  const current = files.find((f) => f.status === "uploading");
  const doneCount = files.filter((f) => f.status === "done").length;

  return (
    <div className="min-h-dvh flex flex-col items-center justify-center p-4">
      <div className="w-full max-w-2xl space-y-6">
        <div className="text-center space-y-2">
          <div className="mx-auto flex size-12 items-center justify-center rounded-2xl bg-accent text-white shadow-lg shadow-accent/25">
            <UploadCloud className="size-6" />
          </div>
          <h1 className="text-2xl font-bold tracking-tight text-ink">Upload files</h1>
          {info && (
            <p className="text-sm text-muted">
              {info.note || "Drop your files below."}
              <span className="block text-xs text-faint mt-1">
                Up to {bytes(info.maxBytes)} per file · link valid until {dateTime(info.expiresAt)}
              </span>
              <span className="block text-[11px] text-faint mt-1">
                Your IP address and browser are recorded with each file you upload.
              </span>
            </p>
          )}
        </div>

        <Card className="p-6 space-y-6">
          {error ? (
            <div className="text-center text-sm text-danger py-6">{error}</div>
          ) : !info ? (
            <div className="flex justify-center py-6">
              <Spinner />
            </div>
          ) : (
            <>
              <div
                onDragOver={(e) => {
                  e.preventDefault();
                  setDragOver(true);
                }}
                onDragLeave={() => setDragOver(false)}
                onDrop={(e) => {
                  e.preventDefault();
                  setDragOver(false);
                  addFiles(e.dataTransfer.files);
                }}
                onClick={() => fileInputRef.current?.click()}
                className={cx(
                  "border-2 border-dashed rounded-xl p-10 flex flex-col items-center justify-center cursor-pointer transition-colors text-center",
                  dragOver ? "border-accent bg-accent/5 ring-4 ring-accent/15" : "border-line hover:border-accent/60 bg-sunken/30",
                )}
              >
                <input
                  ref={fileInputRef}
                  type="file"
                  name="files"
                  multiple
                  hidden
                  onChange={(e) => {
                    addFiles(e.target.files);
                    e.target.value = "";
                  }}
                />
                <UploadCloud className="size-10 text-muted mb-3" />
                <div className="font-semibold text-sm text-ink">Drag and drop files here</div>
                <div className="text-xs text-muted mt-1">or click to browse from your device</div>
              </div>

              {files.length > 0 && (
                <div className="space-y-3">
                  {/* The whole batch at a glance. */}
                  {batchTotal > 0 && (isUploading || doneCount > 0) && (
                    <div className="space-y-1.5 rounded-lg border border-line bg-sunken/30 p-3">
                      <div className="flex items-center justify-between text-xs">
                        <span className="font-medium text-ink">
                          {isUploading ? `Uploading ${doneCount + 1} of ${batch.length}` : `${doneCount} of ${batch.length} uploaded`}
                        </span>
                        <span className="font-mono text-muted">
                          {bytes(batchSent)} / {bytes(batchTotal)} · {Math.round((batchSent / batchTotal) * 100)}%
                        </span>
                      </div>
                      <ProgressBar value={(batchSent / batchTotal) * 100} tone={!isUploading && batchSent >= batchTotal ? "ok" : "accent"} />
                      {current && current.speed > 0 && (
                        <div className="text-right text-[11px] text-muted">
                          {bytes(current.speed)}/s · {remaining((batchTotal - batchSent) / current.speed)}
                        </div>
                      )}
                    </div>
                  )}

                  <div className="flex items-center justify-between text-xs font-medium text-muted">
                    <span>Files ({files.length})</span>
                    {!isUploading && (
                      <button onClick={() => setQueue([])} className="hover:text-danger transition-colors">
                        Clear all
                      </button>
                    )}
                  </div>

                  <div className="divide-y divide-line rounded-lg border border-line max-h-80 overflow-y-auto">
                    {files.map((item) => {
                      const pct = item.status === "done" ? 100 : (item.sent / Math.max(item.file.size, 1)) * 100;
                      return (
                        <div key={item.id} className="space-y-2 p-3 text-xs">
                          <div className="flex items-center justify-between gap-3">
                            <div className="flex min-w-0 items-center gap-2">
                              <FileText className="size-4 shrink-0 text-muted" />
                              <span className="truncate font-mono">{item.file.name}</span>
                              <span className="shrink-0 text-[10px] text-muted">({bytes(item.file.size)})</span>
                            </div>
                            <div className="flex shrink-0 items-center gap-2">
                              {item.status === "done" ? (
                                <span className="flex items-center gap-1 font-medium text-ok">
                                  <Check className="size-3.5" /> Uploaded
                                </span>
                              ) : item.status === "uploading" ? (
                                <span className="font-mono font-semibold text-accent">{Math.floor(pct)}%</span>
                              ) : item.status === "error" ? (
                                <span className="font-medium text-danger">Not uploaded</span>
                              ) : item.status === "canceled" ? (
                                <span className="text-muted">Canceled</span>
                              ) : (
                                <span className="text-muted">Waiting</span>
                              )}
                              {(item.status === "uploading" || item.status === "pending") && (
                                <button
                                  type="button"
                                  onClick={() => cancel(item.id)}
                                  className="rounded p-0.5 text-muted hover:bg-sunken hover:text-danger"
                                  aria-label={`Cancel ${item.file.name}`}
                                  title="Cancel"
                                >
                                  <X className="size-3.5" />
                                </button>
                              )}
                            </div>
                          </div>
                          {(item.status === "uploading" || item.status === "done") && (
                            <ProgressBar value={pct} tone={item.status === "done" ? "ok" : "accent"} />
                          )}
                          {item.status === "uploading" && (
                            <div className="flex justify-between font-mono text-[10px] text-muted">
                              <span>
                                {bytes(item.sent)} of {bytes(item.file.size)}
                              </span>
                              <span>
                                {item.speed > 0 ? `${bytes(item.speed)}/s · ${remaining((item.file.size - item.sent) / item.speed)}` : "starting…"}
                              </span>
                            </div>
                          )}
                          {item.detail && (
                            <div className={cx("text-[11px]", item.status === "error" ? "text-danger" : "text-muted")}>{item.detail}</div>
                          )}
                        </div>
                      );
                    })}
                  </div>

                  <Button
                    variant="primary"
                    busy={isUploading}
                    disabled={pending === 0}
                    onClick={uploadAll}
                    className="w-full justify-center"
                  >
                    {isUploading ? "Uploading…" : `Upload ${pending} file(s)`}
                  </Button>
                </div>
              )}
            </>
          )}
        </Card>
      </div>
    </div>
  );
}

// --- Admin manager -------------------------------------------------------------------

/**
 * What came in through one link, newest first. It reloads whenever the
 * link's upload count changes, so it follows the page's polling.
 */
function UploadsList({ link }: { link: Dropzone }) {
  const [uploads, setUploads] = useState<DropzoneUpload[] | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    api
      .dropzoneUploads(link.id)
      .then((list) => {
        setUploads(list);
        setError("");
      })
      .catch((e) => setError(errorText(e)));
  }, [link.id, link.uploads]);

  if (error) return <div className="text-xs text-danger">{error}</div>;
  if (!uploads) {
    return (
      <div className="flex justify-center py-3">
        <Spinner />
      </div>
    );
  }
  return (
    <ul className="max-h-72 divide-y divide-line overflow-y-auto rounded-md border border-line text-xs">
      {uploads.map((u) => {
        const folder = u.key.slice(0, u.key.lastIndexOf("/") + 1);
        return (
          <li key={u.key + u.uploadedAt} className="space-y-1 px-3 py-2">
            <div className="flex items-center justify-between gap-3">
              <Link
                to={`/buckets/${encodeURIComponent(link.bucket)}${folder ? `?prefix=${encodeURIComponent(folder)}` : ""}`}
                className="truncate font-mono font-medium text-ink hover:text-accent hover:underline"
                title={`Open s3://${link.bucket}/${folder}`}
              >
                {u.name}
              </Link>
              <span className="shrink-0 font-mono text-muted">{bytes(u.size)}</span>
            </div>
            <div className="flex flex-wrap gap-x-3 gap-y-0.5 text-[11px] text-muted">
              <span>{dateTime(u.uploadedAt)}</span>
              {u.remote && <span className="font-mono">IP {u.remote}</span>}
              <span title={u.userAgent || "The browser sent no User-Agent"}>{describeUserAgent(u.userAgent)}</span>
            </div>
          </li>
        );
      })}
    </ul>
  );
}

export function DropzonePage() {
  const [buckets, setBuckets] = useState<Bucket[]>([]);
  const [links, setLinks] = useState<Dropzone[] | null>(null);
  const [createModal, setCreateModal] = useState(false);
  const [creating, setCreating] = useState(false);
  const [selectedBucket, setSelectedBucket] = useState("");
  const [selectedPrefix, setSelectedPrefix] = useState("");
  const [expiryHours, setExpiryHours] = useState("24");
  const [maxSizeMB, setMaxSizeMB] = useState("100");
  const [note, setNote] = useState("");
  const [copiedId, setCopiedId] = useState<string | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<Dropzone | null>(null);
  const [openUploads, setOpenUploads] = useState<Set<string>>(new Set());
  const toast = useToast();

  const load = () =>
    api
      .dropzones()
      .then(setLinks)
      .catch((e) => {
        setLinks([]);
        toast("error", errorText(e));
      });

  // New uploads show up without reloading the page, while it is visible.
  useEffect(() => {
    const timer = setInterval(() => {
      if (document.visibilityState === "visible") api.dropzones().then(setLinks).catch(() => {});
    }, 5000);
    return () => clearInterval(timer);
  }, []);

  function toggleUploads(id: string) {
    setOpenUploads((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }

  useEffect(() => {
    load();
    api
      .buckets()
      .then((list) => {
        setBuckets(list);
        if (list.length > 0) setSelectedBucket((b) => b || list[0].name);
      })
      .catch(() => {});
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  async function createLink() {
    if (!selectedBucket) {
      toast("error", "Please select a bucket.");
      return;
    }
    setCreating(true);
    try {
      const created = await api.createDropzone({
        bucket: selectedBucket,
        prefix: selectedPrefix,
        note: note.trim(),
        expiresIn: Number(expiryHours) * 3600,
        maxBytes: Number(maxSizeMB) * 1024 * 1024,
      });
      setLinks((prev) => [created, ...(prev ?? [])]);
      setCreateModal(false);
      setSelectedPrefix("");
      setNote("");
      toast("ok", "Upload link created.");
    } catch (e) {
      toast("error", errorText(e));
    } finally {
      setCreating(false);
    }
  }

  async function deleteLink(link: Dropzone) {
    try {
      await api.deleteDropzone(link.id);
      setLinks((prev) => (prev ?? []).filter((l) => l.id !== link.id));
      toast("ok", "Upload link deleted. It stopped working immediately.");
    } catch (e) {
      toast("error", errorText(e));
    } finally {
      setDeleteTarget(null);
    }
  }

  function copyLink(link: Dropzone) {
    if (!link.url) return;
    navigator.clipboard.writeText(link.url).then(
      () => {
        setCopiedId(link.id);
        toast("ok", "Upload link copied to clipboard.");
        setTimeout(() => setCopiedId(null), 2000);
      },
      () => toast("error", "Could not copy the link."),
    );
  }

  return (
    <div className="space-y-6 pb-16">
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4">
        <PageHeader
          title="Upload Dropzone Links"
          subtitle="Temporary links that let people without an account upload files into a bucket folder. Uploads never overwrite existing files."
        />
        <Button variant="primary" icon={<Plus className="size-4" />} onClick={() => setCreateModal(true)} className="shrink-0">
          Create Dropzone Link
        </Button>
      </div>

      {links === null ? (
        <div className="flex justify-center py-12">
          <Spinner />
        </div>
      ) : links.length === 0 ? (
        <Card className="py-16 text-center text-muted text-sm space-y-3">
          <UploadCloud className="size-10 mx-auto text-faint" />
          <div className="font-semibold text-ink">No upload dropzones created yet</div>
          <div className="text-xs text-muted max-w-sm mx-auto">
            Create an upload link to let clients, contractors, or web forms upload files to your storage.
          </div>
          <Button variant="primary" icon={<Plus className="size-4" />} onClick={() => setCreateModal(true)}>
            Create First Dropzone
          </Button>
        </Card>
      ) : (
        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
          {links.map((link) => (
            <Card key={link.id} className="p-5 space-y-4">
              <div className="flex items-start justify-between gap-3">
                <div className="min-w-0">
                  <div className="flex items-center gap-2">
                    <span className="font-mono font-semibold text-ink text-sm truncate">
                      s3://{link.bucket}/{link.prefix}
                    </span>
                    <span
                      className={cx(
                        "rounded-full px-2 py-0.5 text-[10px] font-semibold",
                        link.expired ? "bg-danger/10 text-danger" : "bg-ok/10 text-ok",
                      )}
                    >
                      {link.expired ? "Expired" : "Active"}
                    </span>
                  </div>
                  {link.note && <p className="text-xs text-muted mt-1">{link.note}</p>}
                </div>
                <button
                  onClick={() => setDeleteTarget(link)}
                  className="text-muted hover:text-danger transition-colors"
                  title="Delete link"
                  aria-label="Delete link"
                >
                  <Trash2 className="size-4" />
                </button>
              </div>

              <div className="grid grid-cols-3 gap-2 text-xs border-t border-line pt-3 font-mono">
                <div>
                  <span className="text-muted block text-[10px]">Expires:</span>
                  <span>{dateTime(link.expiresAt)}</span>
                </div>
                <div>
                  <span className="text-muted block text-[10px]">Limit:</span>
                  <span>{bytes(link.maxBytes)} per file</span>
                </div>
                <div>
                  <span className="text-muted block text-[10px]">Received:</span>
                  <span className={cx(link.uploads > 0 && "font-semibold text-ink")}>
                    {link.uploads === 0 ? "Nothing yet" : `${link.uploads} file${link.uploads === 1 ? "" : "s"} · ${bytes(link.uploadedBytes)}`}
                  </span>
                  {link.lastUploadAt && <span className="block text-[10px] text-muted">last {dateTime(link.lastUploadAt)}</span>}
                </div>
              </div>

              {link.uploads > 0 && (
                <div className="space-y-2">
                  <button
                    type="button"
                    onClick={() => toggleUploads(link.id)}
                    className="flex items-center gap-1 text-xs font-medium text-accent hover:underline"
                    aria-expanded={openUploads.has(link.id)}
                  >
                    {openUploads.has(link.id) ? <ChevronDown className="size-3.5" /> : <ChevronRight className="size-3.5" />}
                    <FolderOpen className="size-3.5" />
                    {openUploads.has(link.id) ? "Hide uploaded files" : "Show uploaded files"}
                  </button>
                  {openUploads.has(link.id) && <UploadsList link={link} />}
                </div>
              )}

              {!link.expired && link.url && (
                <div className="flex items-center gap-2 pt-2">
                  <Button
                    variant="secondary"
                    icon={copiedId === link.id ? <Check className="size-3.5 text-ok" /> : <Copy className="size-3.5" />}
                    onClick={() => copyLink(link)}
                    className="flex-1 justify-center text-xs"
                  >
                    {copiedId === link.id ? "Copied!" : "Copy Public Link"}
                  </Button>
                  <a href={link.url} target="_blank" rel="noreferrer" className="contents">
                    <Button icon={<ExternalLink className="size-3.5" />} title="Open public view" />
                  </a>
                </div>
              )}
            </Card>
          ))}
        </div>
      )}

      <Modal
        open={createModal}
        onClose={() => !creating && setCreateModal(false)}
        title="Create Dropzone Upload Link"
        footer={
          <>
            <Button disabled={creating} onClick={() => setCreateModal(false)}>
              Cancel
            </Button>
            <Button variant="primary" busy={creating} icon={<Share2 className="size-4" />} onClick={createLink}>
              Generate Link
            </Button>
          </>
        }
      >
        <div className="space-y-4">
          <div>
            <label htmlFor="dropzone-target-bucket" className="label-strip mb-1.5 block">Target Bucket</label>
            <Select
              id="dropzone-target-bucket"
              aria-label="Target Bucket"
              value={selectedBucket}
              onChange={setSelectedBucket}
              options={buckets.map((b) => ({ value: b.name, label: b.name }))}
            />
          </div>

          <div>
            <label htmlFor="dropzone-folder-prefix" className="label-strip mb-1.5 block">Folder (Optional)</label>
            <Input
              id="dropzone-folder-prefix"
              value={selectedPrefix}
              onChange={(e) => setSelectedPrefix(e.target.value)}
              placeholder="e.g. client-submissions/2026/"
              className="font-mono text-xs"
            />
          </div>

          <div className="grid grid-cols-2 gap-3">
            <div>
              <label htmlFor="dropzone-link-expiration" className="label-strip mb-1.5 block">Link Expiration</label>
              <Select
                id="dropzone-link-expiration"
                aria-label="Link Expiration"
                value={expiryHours}
                onChange={setExpiryHours}
                options={[
                  { value: "1", label: "1 Hour" },
                  { value: "24", label: "24 Hours (1 Day)" },
                  { value: "72", label: "3 Days" },
                  { value: "168", label: "7 Days" },
                  { value: "720", label: "30 Days" },
                ]}
              />
            </div>

            <div>
              <label htmlFor="dropzone-max-file-size" className="label-strip mb-1.5 block">Max File Size</label>
              <Select
                id="dropzone-max-file-size"
                aria-label="Max File Size"
                value={maxSizeMB}
                onChange={setMaxSizeMB}
                options={[
                  { value: "25", label: "25 MB" },
                  { value: "100", label: "100 MB" },
                  { value: "500", label: "500 MB" },
                  { value: "2048", label: "2 GB" },
                ]}
              />
            </div>
          </div>

          <div>
            <label htmlFor="dropzone-note-instructions-for-uploader" className="label-strip mb-1.5 block">
              Note / Instructions for Uploader
            </label>
            <Input
              id="dropzone-note-instructions-for-uploader"
              value={note}
              maxLength={500}
              onChange={(e) => setNote(e.target.value)}
              placeholder="e.g. Please upload your documents for verification"
            />
          </div>
        </div>
      </Modal>

      <Modal
        open={!!deleteTarget}
        onClose={() => setDeleteTarget(null)}
        title="Delete upload link"
        footer={
          <>
            <Button onClick={() => setDeleteTarget(null)}>Cancel</Button>
            <Button variant="danger" icon={<Trash2 className="size-4" />} onClick={() => deleteTarget && deleteLink(deleteTarget)}>
              Delete
            </Button>
          </>
        }
      >
        <p className="text-sm text-muted">
          The link stops working immediately. Files already uploaded stay in{" "}
          <span className="font-mono text-ink">
            s3://{deleteTarget?.bucket}/{deleteTarget?.prefix}
          </span>
          .
        </p>
      </Modal>
    </div>
  );
}
