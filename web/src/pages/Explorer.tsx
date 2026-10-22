import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { Link, useParams, useSearchParams } from "react-router";
import {
  ChevronRight,
  Copy,
  Download,
  File,
  FileArchive,
  FileAudio,
  FileCode,
  FileImage,
  FileText,
  FileVideo,
  Folder,
  FolderPlus,
  RefreshCw,
  Search,
  Settings2,
  Share2,
  Trash2,
  Upload,
  X,
} from "lucide-react";
import { api, downloadObject, uploadFile, type Bucket, type ObjectInfo } from "../api";
import { baseName, bytes, count, dateTime, relative } from "../lib/format";
import { Badge, Button, EmptyState, Field, IconButton, Input, Modal, Spinner, cx, errorText, useToast } from "../components/ui";
import { accessLabel } from "./Buckets";

// --- file kinds ------------------------------------------------------------------

type Kind = "image" | "video" | "audio" | "text" | "archive" | "code" | "other";

function kindOf(key: string, contentType = ""): Kind {
  const ext = key.slice(key.lastIndexOf(".") + 1).toLowerCase();
  if (contentType.startsWith("image/") || ["jpg", "jpeg", "png", "gif", "webp", "avif", "svg", "bmp", "ico"].includes(ext)) return "image";
  if (contentType.startsWith("video/") || ["mp4", "webm", "mov", "m4v", "mkv"].includes(ext)) return "video";
  if (contentType.startsWith("audio/") || ["mp3", "wav", "ogg", "flac", "m4a"].includes(ext)) return "audio";
  if (["zip", "gz", "tgz", "tar", "7z", "rar", "xz", "zst"].includes(ext)) return "archive";
  if (["js", "ts", "tsx", "go", "py", "rs", "java", "c", "h", "sh", "sql", "html", "css", "yml", "yaml", "toml"].includes(ext)) return "code";
  if (contentType.startsWith("text/") || ["txt", "md", "csv", "log", "json", "xml", "ini", "conf", "env"].includes(ext)) return "text";
  return "other";
}

const kindIcons: Record<Kind, typeof File> = {
  image: FileImage,
  video: FileVideo,
  audio: FileAudio,
  text: FileText,
  archive: FileArchive,
  code: FileCode,
  other: File,
};

// --- uploads ---------------------------------------------------------------------------

interface UploadJob {
  id: number;
  name: string;
  size: number;
  sent: number;
  state: "uploading" | "done" | "error" | "cancelled";
  error?: string;
  abort: AbortController;
}

// Once every upload has finished the panel closes itself: soon when all went
// well, later when something failed (so the error can be read), and never
// while the pointer is over it.
const CLOSE_AFTER_MS = 4000;
const CLOSE_AFTER_FAILURE_MS = 10000;
const FADE_MS = 300;

function UploadPanel({ jobs, onClear }: { jobs: UploadJob[]; onClear: () => void }) {
  const [hover, setHover] = useState(false);
  const [leaving, setLeaving] = useState(false);
  const onClearRef = useRef(onClear);
  onClearRef.current = onClear;
  const active = jobs.filter((j) => j.state === "uploading").length;
  const failed = jobs.some((j) => j.state === "error");

  useEffect(() => {
    setLeaving(false);
    if (jobs.length === 0 || active > 0 || hover) return;
    const t = setTimeout(() => setLeaving(true), failed ? CLOSE_AFTER_FAILURE_MS : CLOSE_AFTER_MS);
    return () => clearTimeout(t);
  }, [jobs, active, hover, failed]);
  useEffect(() => {
    if (!leaving) return;
    const t = setTimeout(() => onClearRef.current(), FADE_MS);
    return () => clearTimeout(t);
  }, [leaving]);

  if (jobs.length === 0) return null;
  return (
    <div
      onMouseEnter={() => setHover(true)}
      onMouseLeave={() => setHover(false)}
      style={{ transitionDuration: `${FADE_MS}ms` }}
      className={cx(
        "fixed right-4 bottom-4 left-4 z-40 rounded-card border border-line bg-surface shadow-xl transition-all sm:left-auto sm:w-96",
        leaving && "translate-y-2 opacity-0",
      )}
    >
      <div className="flex items-center justify-between border-b border-line px-4 py-2.5">
        <span className="text-sm font-medium">{active ? `Uploading ${active} file${active > 1 ? "s" : ""}…` : "Uploads finished"}</span>
        {!active && (
          <IconButton label="Close" onClick={onClear} className="size-7">
            <X className="size-4" />
          </IconButton>
        )}
      </div>
      <ul className="max-h-64 divide-y divide-line overflow-y-auto">
        {jobs.map((j) => {
          const pct = j.size ? Math.round((j.sent / j.size) * 100) : 100;
          return (
            <li key={j.id} className="px-4 py-2.5">
              <div className="flex items-center justify-between gap-3 text-sm">
                <span className="truncate font-mono text-xs">{j.name}</span>
                {j.state === "uploading" ? (
                  <button className="text-xs text-muted hover:text-danger" onClick={() => j.abort.abort()}>
                    Cancel
                  </button>
                ) : (
                  <span className={cx("text-xs", j.state === "done" ? "text-ok" : "text-danger")}>
                    {j.state === "done" ? "Done" : j.state === "cancelled" ? "Cancelled" : "Failed"}
                  </span>
                )}
              </div>
              <div className="mt-1.5 flex items-center gap-2">
                <div className="h-1.5 flex-1 overflow-hidden rounded-full bg-sunken">
                  <div
                    className={cx("h-full transition-all", j.state === "error" || j.state === "cancelled" ? "bg-danger" : j.state === "done" ? "bg-ok" : "bg-accent")}
                    style={{ width: `${pct}%` }}
                  />
                </div>
                <span className="num w-20 text-right text-[11px] text-muted">
                  {j.state === "uploading" ? `${pct}% of ${bytes(j.size)}` : bytes(j.size)}
                </span>
              </div>
              {j.error && <div className="mt-1 text-xs text-danger">{j.error}</div>}
            </li>
          );
        })}
      </ul>
    </div>
  );
}

// --- share dialog ----------------------------------------------------------------------

const EXPIRY_CHOICES = [
  { label: "1 hour", seconds: 3600 },
  { label: "1 day", seconds: 86400 },
  { label: "7 days", seconds: 7 * 86400 },
];

function ShareModal({ bucket, object, onClose }: { bucket: string; object: ObjectInfo | null; onClose: () => void }) {
  const [expires, setExpires] = useState(86400);
  const [download, setDownload] = useState(false);
  const [link, setLink] = useState<{ url: string; expiresAt: string } | null>(null);
  const [busy, setBusy] = useState(false);
  const [max, setMax] = useState(7 * 86400);
  const toast = useToast();

  useEffect(() => {
    if (!object) return;
    setLink(null);
    api.settings().then((s) => {
      setMax(s.shareMaxExpirySeconds);
      setExpires((e) => Math.min(e, s.shareMaxExpirySeconds));
    });
  }, [object]);

  async function create() {
    if (!object) return;
    setBusy(true);
    try {
      setLink(await api.share(bucket, object.key, expires, download));
    } catch (e) {
      toast("error", errorText(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal
      open={!!object}
      onClose={onClose}
      title="Share link"
      footer={
        link ? (
          <Button onClick={onClose}>Done</Button>
        ) : (
          <>
            <Button onClick={onClose}>Cancel</Button>
            <Button variant="primary" busy={busy} onClick={create} icon={<Share2 className="size-4" />}>
              Create link
            </Button>
          </>
        )
      }
    >
      {object && (
        <div className="space-y-4">
          <p className="text-sm text-muted">
            Anyone with the link can download <span className="font-mono text-ink">{baseName(object.key)}</span> until it expires, without signing in.
          </p>
          {!link ? (
            <>
              <div>
                <div className="mb-1.5 text-sm font-medium">Expires after</div>
                <div className="flex flex-wrap gap-2">
                  {EXPIRY_CHOICES.filter((c) => c.seconds <= max).map((c) => (
                    <button
                      key={c.seconds}
                      onClick={() => setExpires(c.seconds)}
                      className={cx(
                        "h-8 rounded-md border px-3 text-sm",
                        expires === c.seconds ? "border-accent bg-accent-soft text-accent-ink" : "border-line-strong hover:bg-sunken",
                      )}
                    >
                      {c.label}
                    </button>
                  ))}
                </div>
                <p className="mt-1.5 text-xs text-muted">The maximum allowed is set in Settings.</p>
              </div>
              <label className="flex items-center gap-2 text-sm">
                <input type="checkbox" checked={download} onChange={(e) => setDownload(e.target.checked)} className="size-4 accent-[var(--accent)]" />
                Download instead of opening in the browser
              </label>
            </>
          ) : (
            <>
              <div className="flex gap-2">
                <Input mono readOnly value={link.url} onFocus={(e) => e.target.select()} className="text-xs" />
                <Button
                  icon={<Copy className="size-4" />}
                  onClick={() => navigator.clipboard.writeText(link.url).then(() => toast("ok", "Link copied."))}
                >
                  Copy
                </Button>
              </div>
              <p className="text-xs text-muted">
                Expires {dateTime(link.expiresAt)}. To cut every link short, use “Revoke all share links” in Settings.
              </p>
            </>
          )}
        </div>
      )}
    </Modal>
  );
}

// --- detail drawer -----------------------------------------------------------------------

function TextPreview({ url }: { url: string }) {
  const [text, setText] = useState<string | null>(null);
  useEffect(() => {
    fetch(url, { headers: { Range: "bytes=0-262143" } })
      .then((r) => r.text())
      .then(setText)
      .catch(() => setText("(preview unavailable)"));
  }, [url]);
  if (text === null) return <Spinner className="m-6" />;
  return <pre className="max-h-80 overflow-auto p-3 font-mono text-xs leading-relaxed whitespace-pre-wrap text-ink">{text}</pre>;
}

function Preview({ bucket, object }: { bucket: string; object: ObjectInfo }) {
  const kind = kindOf(object.key, object.contentType);
  const url = api.downloadURL(bucket, object.key, true);
  const Icon = kindIcons[kind];
  let body: ReactNode;
  if (kind === "image") body = <img src={url} alt={object.key} className="mx-auto max-h-80 object-contain" />;
  else if (kind === "video") body = <video src={url} controls className="max-h-80 w-full bg-black" />;
  else if (kind === "audio") body = <audio src={url} controls className="w-full p-3" />;
  else if ((kind === "text" || kind === "code") && object.size > 0) body = <TextPreview url={url} />;
  else
    body = (
      <div className="flex flex-col items-center gap-2 py-10 text-muted">
        <Icon className="size-10 text-faint" />
        <span className="text-xs">No preview for this file type</span>
      </div>
    );
  return <div className="overflow-hidden rounded-md border border-line bg-sunken">{body}</div>;
}

function Detail({
  bucket,
  objectKey,
  onClose,
  onShare,
  onDeleted,
}: {
  bucket: string;
  objectKey: string;
  onClose: () => void;
  onShare: (o: ObjectInfo) => void;
  onDeleted: () => void;
}) {
  const [info, setInfo] = useState<ObjectInfo | null>(null);
  const [confirm, setConfirm] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [downloaded, setDownloaded] = useState<number | null>(null); // bytes so far; null when idle
  const toast = useToast();

  async function download(o: ObjectInfo) {
    setDownloaded(0);
    try {
      await downloadObject(bucket, o, setDownloaded);
    } catch (e) {
      toast("error", errorText(e));
    } finally {
      setDownloaded(null);
    }
  }

  async function remove() {
    setDeleting(true);
    try {
      await api.deleteObjects(bucket, [objectKey], []);
      toast("ok", `${baseName(objectKey)} deleted.`);
      onDeleted();
    } catch (e) {
      toast("error", errorText(e));
      setDeleting(false);
    }
  }
  useEffect(() => {
    setInfo(null);
    api.stat(bucket, objectKey).then(setInfo).catch((e) => toast("error", errorText(e)));
  }, [bucket, objectKey, toast]);

  const rows: [string, ReactNode][] = info
    ? [
        ["Size", <span className="num">{bytes(info.size)} ({count(info.size)} B)</span>],
        ["Type", <span className="font-mono">{info.contentType || "—"}</span>],
        ["Modified", dateTime(info.lastModified)],
        ["ETag", <span className="font-mono break-all">{info.etag}</span>],
        ...(info.partsCount ? [["Parts", <span className="num">{info.partsCount}</span>] as [string, ReactNode]] : []),
        ...Object.entries(info.checksums ?? {}).map(([a, v]) => [a, <span className="font-mono break-all">{v}</span>] as [string, ReactNode]),
        ...Object.entries(info.metadata ?? {}).map(([k, v]) => [`meta: ${k}`, <span className="font-mono break-all">{v}</span>] as [string, ReactNode]),
      ]
    : [];

  return (
    <aside className="fixed inset-0 z-30 flex flex-col bg-surface sm:inset-y-0 sm:right-0 sm:left-auto sm:w-[26rem] sm:border-l sm:border-line sm:shadow-2xl">
      <div className="flex items-center justify-between gap-3 border-b border-line px-5 py-3.5">
        <div className="min-w-0">
          <div className="label-strip">object</div>
          <div className="truncate font-mono text-sm font-medium" title={objectKey}>
            {baseName(objectKey)}
          </div>
        </div>
        <IconButton label="Close" onClick={onClose}>
          <X className="size-4" />
        </IconButton>
      </div>
      <div className="flex-1 space-y-5 overflow-y-auto px-5 py-4">
        {!info ? (
          <Spinner className="mx-auto my-10" />
        ) : (
          <>
            <Preview bucket={bucket} object={info} />
            <div className="grid grid-cols-3 gap-2">
              <Button icon={<Download className="size-4" />} busy={downloaded !== null} disabled={deleting} onClick={() => download(info)}>
                {downloaded === null ? "Download" : info.size ? `${Math.floor((downloaded / info.size) * 100)}%` : "Download"}
              </Button>
              <Button icon={<Share2 className="size-4" />} disabled={deleting} onClick={() => onShare(info)}>
                Share
              </Button>
              <Button variant="danger" icon={<Trash2 className="size-4" />} busy={deleting} onClick={() => setConfirm(true)}>
                Delete
              </Button>
            </div>
            <dl className="divide-y divide-line rounded-md border border-line text-sm">
              {rows.map(([k, v]) => (
                <div key={k} className="grid grid-cols-[7rem_1fr] gap-3 px-3 py-2">
                  <dt className="text-muted">{k}</dt>
                  <dd className="min-w-0 text-xs leading-5">{v}</dd>
                </div>
              ))}
            </dl>
            <div className="text-xs text-muted">
              Key <span className="font-mono break-all text-ink">{info.key}</span>
            </div>
          </>
        )}
      </div>
      <Modal
        open={confirm}
        onClose={() => !deleting && setConfirm(false)}
        title="Delete object"
        footer={
          <>
            <Button disabled={deleting} onClick={() => setConfirm(false)}>
              Cancel
            </Button>
            <Button variant="danger" icon={<Trash2 className="size-4" />} busy={deleting} onClick={remove}>
              {deleting ? "Deleting…" : "Delete"}
            </Button>
          </>
        }
      >
        <p className="text-sm">
          Delete <span className="font-mono">{objectKey}</span>? This cannot be undone.
        </p>
      </Modal>
    </aside>
  );
}

// --- page ------------------------------------------------------------------------------------

export function ExplorerPage() {
  const bucket = useParams().bucket!;
  const [params, setParams] = useSearchParams();
  const prefix = params.get("prefix") ?? "";
  const [meta, setMeta] = useState<Bucket | null>(null);
  const [objects, setObjects] = useState<ObjectInfo[]>([]);
  const [prefixes, setPrefixes] = useState<string[]>([]);
  const [next, setNext] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [search, setSearch] = useState("");
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [detail, setDetail] = useState<string | null>(null);
  const [sharing, setSharing] = useState<ObjectInfo | null>(null);
  const [folderOpen, setFolderOpen] = useState(false);
  const [folderName, setFolderName] = useState("");
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [deletingSelected, setDeletingSelected] = useState(false);
  const [jobs, setJobs] = useState<UploadJob[]>([]);
  const [dragging, setDragging] = useState(false);
  const fileInput = useRef<HTMLInputElement>(null);
  const searchInput = useRef<HTMLInputElement>(null);
  const toast = useToast();

  const load = useCallback(
    async (after?: string) => {
      setLoading(true);
      // A fresh listing also refreshes the object count and size in the
      // header, so they follow uploads, deletes and the refresh button.
      if (!after) api.bucket(bucket).then(setMeta).catch((e) => toast("error", errorText(e)));
      try {
        const page = await api.objects(bucket, { prefix, after, search: search || undefined, limit: 200 });
        const files = page.objects.filter((o) => o.key !== prefix); // the folder marker itself
        setObjects((cur) => (after ? [...cur, ...files] : files));
        setPrefixes((cur) => (after ? [...cur, ...page.prefixes] : page.prefixes));
        setNext(page.truncated ? (page.nextAfter ?? null) : null);
      } catch (e) {
        toast("error", errorText(e));
      } finally {
        setLoading(false);
      }
    },
    [bucket, prefix, search, toast],
  );

  useEffect(() => {
    setSelected(new Set());
    const t = setTimeout(() => load(), search ? 250 : 0);
    return () => clearTimeout(t);
  }, [load, search]);

  // Keyboard shortcuts: "/" focuses search, "u" opens the file picker.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const typing = e.target instanceof HTMLInputElement || e.target instanceof HTMLTextAreaElement;
      if (typing || e.metaKey || e.ctrlKey || e.altKey) return;
      if (e.key === "/") {
        e.preventDefault();
        searchInput.current?.focus();
      } else if (e.key === "u") {
        fileInput.current?.click();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  function upload(files: FileList | File[]) {
    for (const file of Array.from(files)) {
      const job: UploadJob = { id: Math.random(), name: file.name, size: file.size, sent: 0, state: "uploading", abort: new AbortController() };
      setJobs((j) => [...j, job]);
      const update = (patch: Partial<UploadJob>) => setJobs((all) => all.map((x) => (x.id === job.id ? { ...x, ...patch } : x)));
      uploadFile(bucket, prefix + file.name, file, (sent) => update({ sent }), job.abort.signal)
        .then(() => {
          update({ state: "done", sent: file.size });
          load();
        })
        .catch((e) => update({ state: job.abort.signal.aborted ? "cancelled" : "error", error: job.abort.signal.aborted ? undefined : errorText(e) }));
    }
  }

  // Drag and drop of files onto the whole window. dragenter/dragleave fire for
  // every child element and not at all in some cases (leaving the window,
  // Esc), so the overlay instead stays up while dragover keeps arriving: the
  // browser repeats it at least every ~550 ms while a drag is over the page.
  const uploadRef = useRef(upload);
  uploadRef.current = upload;
  useEffect(() => {
    let hide: ReturnType<typeof setTimeout> | undefined;
    const hasFiles = (e: globalThis.DragEvent) => !!e.dataTransfer?.types.includes("Files");
    const stop = () => {
      clearTimeout(hide);
      setDragging(false);
    };
    const onOver = (e: globalThis.DragEvent) => {
      if (!hasFiles(e)) return;
      e.preventDefault(); // allows the drop
      setDragging(true);
      clearTimeout(hide);
      hide = setTimeout(stop, 800);
    };
    const onLeave = (e: globalThis.DragEvent) => {
      if (!e.relatedTarget) stop(); // left the window
    };
    const onDrop = (e: globalThis.DragEvent) => {
      if (!hasFiles(e)) return;
      e.preventDefault(); // keeps the browser from opening the file
      stop();
      if (e.dataTransfer?.files.length) uploadRef.current(e.dataTransfer.files);
    };
    window.addEventListener("dragover", onOver);
    window.addEventListener("dragleave", onLeave);
    window.addEventListener("drop", onDrop);
    window.addEventListener("dragend", stop);
    return () => {
      stop();
      window.removeEventListener("dragover", onOver);
      window.removeEventListener("dragleave", onLeave);
      window.removeEventListener("drop", onDrop);
      window.removeEventListener("dragend", stop);
    };
  }, []);

  const crumbs = useMemo(() => {
    const parts = prefix.split("/").filter(Boolean);
    return parts.map((p, i) => ({ name: p, prefix: parts.slice(0, i + 1).join("/") + "/" }));
  }, [prefix]);

  const goTo = (p: string) => {
    setSearch("");
    setParams(p ? { prefix: p } : {});
  };
  const toggle = (k: string) =>
    setSelected((s) => {
      const n = new Set(s);
      if (n.has(k)) n.delete(k);
      else n.add(k);
      return n;
    });
  const allKeys = [...prefixes, ...objects.map((o) => o.key)];
  const allSelected = allKeys.length > 0 && allKeys.every((k) => selected.has(k));

  async function deleteSelected() {
    const keys = [...selected].filter((k) => !k.endsWith("/"));
    const folders = [...selected].filter((k) => k.endsWith("/"));
    try {
      setDeletingSelected(true);
      const { deleted } = await api.deleteObjects(bucket, keys, folders);
      toast("ok", `${count(deleted)} object${deleted === 1 ? "" : "s"} deleted.`);
      setConfirmDelete(false);
      setSelected(new Set());
      load();
    } catch (e) {
      toast("error", errorText(e));
    } finally {
      setDeletingSelected(false);
    }
  }

  async function createFolder() {
    const name = folderName.trim().replace(/^\/+|\/+$/g, "");
    if (!name) return;
    try {
      await api.createFolder(bucket, prefix + name + "/");
      setFolderOpen(false);
      setFolderName("");
      goTo(prefix + name + "/");
    } catch (e) {
      toast("error", errorText(e));
    }
  }

  const access = meta ? accessLabel(meta) : null;
  const empty = !loading && objects.length === 0 && prefixes.length === 0;

  return (
    <div className="relative">
      {/* Header */}
      <div className="mb-5 flex flex-col gap-4 lg:flex-row lg:items-end lg:justify-between">
        <div className="min-w-0">
          <Link to="/buckets" className="label-strip hover:text-ink">
            ← buckets
          </Link>
          <h1 className="mt-1 truncate font-mono text-2xl font-medium tracking-tight">{bucket}</h1>
          {meta && access && (
            <div className="label-strip mt-2 flex flex-wrap items-center gap-x-3 gap-y-1">
              <span>{count(meta.objects)} objects</span>
              <span>·</span>
              <span>{bytes(meta.size)}</span>
              {meta.quotaBytes > 0 && (
                <>
                  <span>·</span>
                  <span>quota {bytes(meta.quotaBytes)}</span>
                </>
              )}
              <Badge tone={access.tone}>
                <access.icon className="size-3" />
                {access.text}
              </Badge>
            </div>
          )}
        </div>
        <div className="flex flex-wrap gap-2">
          <Button variant="primary" icon={<Upload className="size-4" />} onClick={() => fileInput.current?.click()}>
            Upload
          </Button>
          <Button icon={<FolderPlus className="size-4" />} onClick={() => setFolderOpen(true)}>
            New folder
          </Button>
          <Link to={`/buckets/${encodeURIComponent(bucket)}/settings`} className="contents">
            <Button icon={<Settings2 className="size-4" />}>Settings</Button>
          </Link>
        </div>
      </div>
      <input ref={fileInput} type="file" multiple hidden onChange={(e) => e.target.files && upload(e.target.files)} />

      {/* Toolbar */}
      <div className="mb-3 flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
        <nav className="flex min-w-0 flex-wrap items-center gap-1 font-mono text-sm" aria-label="Folders">
          <button onClick={() => goTo("")} className={cx("rounded px-1.5 py-0.5 hover:bg-sunken", !prefix && "font-medium text-ink")}>
            {bucket}
          </button>
          {crumbs.map((c, i) => (
            <span key={c.prefix} className="flex items-center gap-1">
              <ChevronRight className="size-3.5 text-faint" />
              <button
                onClick={() => goTo(c.prefix)}
                className={cx("max-w-48 truncate rounded px-1.5 py-0.5 hover:bg-sunken", i === crumbs.length - 1 ? "font-medium text-ink" : "text-muted")}
              >
                {c.name}
              </button>
            </span>
          ))}
        </nav>
        <div className="flex items-center gap-2">
          <div className="relative w-full sm:w-64">
            <Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-faint" />
            <Input ref={searchInput} value={search} onChange={(e) => setSearch(e.target.value)} placeholder={prefix ? "Search in this folder" : "Search objects"} className="pr-9 pl-9" />
            <kbd className="pointer-events-none absolute top-1/2 right-2.5 hidden -translate-y-1/2 rounded border border-line px-1 font-mono text-[10px] text-faint sm:block">/</kbd>
          </div>
          <IconButton label="Refresh" onClick={() => load()}>
            <RefreshCw className={cx("size-4", loading && "animate-spin")} />
          </IconButton>
        </div>
      </div>

      {/* Selection bar */}
      {selected.size > 0 && (
        <div className="mb-3 flex items-center justify-between rounded-md border border-accent/30 bg-accent-soft px-4 py-2 text-sm">
          <span className="font-medium text-accent-ink">{count(selected.size)} selected</span>
          <div className="flex gap-2">
            <Button variant="ghost" onClick={() => setSelected(new Set())}>
              Clear
            </Button>
            <Button variant="danger" icon={<Trash2 className="size-4" />} busy={deletingSelected} onClick={() => setConfirmDelete(true)}>
              Delete
            </Button>
          </div>
        </div>
      )}

      {/* Listing */}
      <div className="overflow-hidden rounded-card border border-line bg-surface">
        {empty ? (
          <EmptyState
            title={search ? "Nothing matches" : prefix ? "This folder is empty" : "This bucket is empty"}
            body={search ? `No object under this folder contains “${search}”.` : "Drop files anywhere on this page, or use the Upload button."}
            action={
              !search && (
                <Button variant="primary" icon={<Upload className="size-4" />} onClick={() => fileInput.current?.click()}>
                  Upload files
                </Button>
              )
            }
          />
        ) : (
          <table className="w-full text-sm">
            <thead className="border-b border-line bg-sunken/60 text-left">
              <tr className="label-strip">
                <th className="w-10 py-2 pl-4">
                  <input
                    type="checkbox"
                    aria-label="Select all"
                    checked={allSelected}
                    onChange={() => setSelected(allSelected ? new Set() : new Set(allKeys))}
                    className="size-4 accent-[var(--accent)]"
                  />
                </th>
                <th className="py-2 pr-3 font-normal">name</th>
                <th className="hidden w-28 py-2 pr-3 text-right font-normal sm:table-cell">size</th>
                <th className="hidden w-40 py-2 pr-4 font-normal md:table-cell">modified</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-line">
              {prefixes.map((p) => (
                <tr key={p} className="group hover:bg-sunken/60">
                  <td className="py-2.5 pl-4">
                    <input type="checkbox" aria-label={`Select ${p}`} checked={selected.has(p)} onChange={() => toggle(p)} className="size-4 accent-[var(--accent)]" />
                  </td>
                  <td className="py-2.5 pr-3">
                    <button onClick={() => goTo(p)} className="flex max-w-full items-center gap-2.5 text-left">
                      <Folder className="size-4 shrink-0 fill-accent-soft text-accent" />
                      <span className="truncate font-medium group-hover:text-accent-ink">{search ? p : baseName(p)}/</span>
                    </button>
                  </td>
                  <td className="hidden py-2.5 pr-3 text-right text-faint sm:table-cell">—</td>
                  <td className="hidden py-2.5 pr-4 text-faint md:table-cell">—</td>
                </tr>
              ))}
              {objects.map((o) => {
                const Icon = kindIcons[kindOf(o.key)];
                return (
                  <tr key={o.key} className={cx("group cursor-pointer hover:bg-sunken/60", detail === o.key && "bg-accent-soft/60")} onClick={() => setDetail(o.key)}>
                    <td className="py-2.5 pl-4" onClick={(e) => e.stopPropagation()}>
                      <input type="checkbox" aria-label={`Select ${o.key}`} checked={selected.has(o.key)} onChange={() => toggle(o.key)} className="size-4 accent-[var(--accent)]" />
                    </td>
                    <td className="max-w-0 py-2.5 pr-3">
                      <div className="flex items-center gap-2.5">
                        <Icon className="size-4 shrink-0 text-faint" />
                        <span className="truncate" title={o.key}>
                          {search ? o.key : baseName(o.key)}
                        </span>
                      </div>
                      <div className="num mt-0.5 pl-6.5 text-[11px] text-muted sm:hidden">
                        {bytes(o.size)} · {relative(o.lastModified)}
                      </div>
                    </td>
                    <td className="num hidden py-2.5 pr-3 text-right text-muted sm:table-cell">{bytes(o.size)}</td>
                    <td className="hidden py-2.5 pr-4 text-muted md:table-cell" title={dateTime(o.lastModified)}>
                      {relative(o.lastModified)}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        )}
        {loading && objects.length + prefixes.length === 0 && !empty && (
          <div className="flex justify-center py-10">
            <Spinner />
          </div>
        )}
        {next && (
          <div className="border-t border-line p-3 text-center">
            <Button variant="ghost" busy={loading} onClick={() => load(next)}>
              Load more
            </Button>
          </div>
        )}
      </div>

      {/* Drop overlay */}
      {dragging && (
        <div className="pointer-events-none fixed inset-0 z-50 flex items-center justify-center bg-accent/10 p-6 backdrop-blur-[1px]">
          <div className="rounded-card border-2 border-dashed border-accent bg-surface px-10 py-8 text-center shadow-xl">
            <Upload className="mx-auto mb-3 size-8 text-accent" />
            <div className="font-medium">Drop to upload</div>
            <div className="mt-1 font-mono text-xs text-muted">
              to {bucket}/{prefix}
            </div>
          </div>
        </div>
      )}

      {detail && (
        <Detail
          bucket={bucket}
          objectKey={detail}
          onClose={() => setDetail(null)}
          onShare={setSharing}
          onDeleted={() => {
            setDetail(null);
            load();
          }}
        />
      )}
      <ShareModal bucket={bucket} object={sharing} onClose={() => setSharing(null)} />
      <UploadPanel jobs={jobs} onClear={() => setJobs([])} />

      <Modal
        open={folderOpen}
        onClose={() => setFolderOpen(false)}
        title="New folder"
        footer={
          <>
            <Button onClick={() => setFolderOpen(false)}>Cancel</Button>
            <Button variant="primary" disabled={!folderName.trim()} onClick={createFolder}>
              Create folder
            </Button>
          </>
        }
      >
        <Field label="Folder name" hint={<span className="font-mono">{`${bucket}/${prefix}${folderName || "…"}/`}</span>}>
          <Input autoFocus value={folderName} onChange={(e) => setFolderName(e.target.value)} onKeyDown={(e) => e.key === "Enter" && createFolder()} />
        </Field>
      </Modal>

      <Modal
        open={confirmDelete}
        onClose={() => !deletingSelected && setConfirmDelete(false)}
        title="Delete objects"
        footer={
          <>
            <Button disabled={deletingSelected} onClick={() => setConfirmDelete(false)}>
              Cancel
            </Button>
            <Button variant="danger" icon={<Trash2 className="size-4" />} busy={deletingSelected} onClick={deleteSelected}>
              {deletingSelected ? "Deleting…" : `Delete ${count(selected.size)}`}
            </Button>
          </>
        }
      >
        <p className="text-sm">
          Delete {count(selected.size)} selected item{selected.size === 1 ? "" : "s"}?
          {[...selected].some((k) => k.endsWith("/")) && " Folders are deleted with everything inside them."} This cannot be undone.
        </p>
      </Modal>
    </div>
  );
}
