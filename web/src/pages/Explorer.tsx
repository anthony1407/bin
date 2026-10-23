import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router";
import {
  Archive,
  BarChart3,
  Check,
  ChevronLeft,
  Code2,
  Copy,
  Download,
  ExternalLink,
  Eye,
  File,
  FileArchive,
  FileAudio,
  FileCode,
  FileImage,
  FileText,
  FileVideo,
  Filter,
  Folder,
  FolderInput,
  FolderPlus,
  GitCompare,
  History,
  Info,
  LayoutGrid,
  LayoutList,
  Maximize2,
  Plus,
  RefreshCw,
  RotateCcw,
  Save,
  Search,
  Settings2,
  Share2,
  Tag,
  Trash2,
  Undo2,
  Upload,
  X,
} from "lucide-react";
import JSZip from "jszip";
import Editor, { DiffEditor } from "../components/CodeEditor";
import { api, downloadObject, uploadFile, type Bucket, type ObjectInfo } from "../api";
import { baseName, bytes, count, dateTime, relative } from "../lib/format";
import { BinMark, Button, EmptyState, Field, IconButton, Input, Modal, Spinner, cx, errorText, useToast } from "../components/ui";
import { Select } from "../components/Select";
import { accessLabel } from "./Buckets";

// --- file kinds ------------------------------------------------------------------

type Kind = "image" | "video" | "audio" | "pdf" | "text" | "archive" | "code" | "other";

function kindOf(key: string, contentType = ""): Kind {
  const ext = key.slice(key.lastIndexOf(".") + 1).toLowerCase();
  if (ext === "pdf" || contentType.includes("pdf")) return "pdf";
  if (contentType.startsWith("image/") || ["jpg", "jpeg", "png", "gif", "webp", "avif", "svg", "bmp", "ico"].includes(ext)) return "image";
  if (contentType.startsWith("video/") || ["mp4", "webm", "mov", "m4v", "mkv"].includes(ext)) return "video";
  if (contentType.startsWith("audio/") || ["mp3", "wav", "ogg", "flac", "m4a"].includes(ext)) return "audio";
  if (["zip", "gz", "tgz", "tar", "7z", "rar", "xz", "zst"].includes(ext)) return "archive";
  if (["js", "ts", "tsx", "go", "py", "rs", "java", "c", "h", "sh", "sql", "html", "css", "yml", "yaml", "toml"].includes(ext)) return "code";
  if (contentType.startsWith("text/") || ["txt", "md", "csv", "log", "json", "xml", "ini", "conf", "env"].includes(ext)) return "text";
  return "other";
}

function getMonacoLanguage(key: string, contentType = ""): string {
  const ext = key.slice(key.lastIndexOf(".") + 1).toLowerCase();
  if (ext === "json") return "json";
  if (["js", "jsx", "mjs", "cjs"].includes(ext)) return "javascript";
  if (["ts", "tsx"].includes(ext)) return "typescript";
  if (["py", "pyw"].includes(ext)) return "python";
  if (ext === "go") return "go";
  if (ext === "rs") return "rust";
  if (["html", "htm"].includes(ext)) return "html";
  if (["css", "scss", "less"].includes(ext)) return "css";
  if (["yml", "yaml"].includes(ext)) return "yaml";
  if (ext === "sql") return "sql";
  if (["sh", "bash", "zsh"].includes(ext)) return "shell";
  if (["xml", "svg"].includes(ext)) return "xml";
  if (["md", "markdown"].includes(ext)) return "markdown";
  if (ext === "c" || ext === "h") return "c";
  if (ext === "cpp" || ext === "hpp" || ext === "cc") return "cpp";
  if (ext === "java") return "java";
  if (contentType.includes("json")) return "json";
  if (contentType.includes("xml")) return "xml";
  if (contentType.includes("html")) return "html";
  return "plaintext";
}

const kindIcons: Record<Kind, typeof File> = {
  image: FileImage,
  video: FileVideo,
  audio: FileAudio,
  pdf: FileText,
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
  { label: "15 min", seconds: 900 },
  { label: "1 hour", seconds: 3600 },
  { label: "12 hours", seconds: 43200 },
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
                <input type="checkbox" name="download" checked={download} onChange={(e) => setDownload(e.target.checked)} className="size-4 accent-[var(--accent)]" />
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

const MAX_PREVIEW_BYTES = 512 * 1024; // 512 KiB text/code preview limit

function MonacoPreview({
  bucket,
  url,
  objectKey,
  contentType,
  onSaved,
}: {
  bucket?: string;
  url: string;
  objectKey: string;
  contentType: string;
  onSaved?: () => void;
}) {
  const [content, setContent] = useState<string | null>(null);
  const [currentVal, setCurrentVal] = useState<string>("");
  const [isEditing, setIsEditing] = useState(false);
  const [showDiff, setShowDiff] = useState(false);
  const [saving, setSaving] = useState(false);
  const toast = useToast();

  useEffect(() => {
    fetch(url, { headers: { Range: `bytes=0-${MAX_PREVIEW_BYTES - 1}` } })
      .then((r) => r.text())
      .then((txt) => {
        setContent(txt);
        setCurrentVal(txt);
      })
      .catch(() => {
        setContent("(preview unavailable)");
        setCurrentVal("");
      });
  }, [url]);

  async function handleSave() {
    if (!bucket) return;
    setSaving(true);
    try {
      const blob = new Blob([currentVal], { type: contentType || "text/plain" });
      const f = Object.assign(blob, { name: baseName(objectKey) }) as unknown as File;
      const abortCtrl = new AbortController();
      await uploadFile(bucket, objectKey, f, () => {}, abortCtrl.signal);
      setContent(currentVal);
      setIsEditing(false);
      setShowDiff(false);
      toast("ok", `Saved changes to ${baseName(objectKey)} on S3.`);
      onSaved?.();
    } catch (e: any) {
      toast("error", e.message || "Failed to save file.");
    } finally {
      setSaving(false);
    }
  }

  if (content === null) return <Spinner className="m-6" />;
  const lang = getMonacoLanguage(objectKey, contentType);
  const isDirty = isEditing && currentVal !== content;

  return (
    <div className="overflow-hidden rounded-md border border-line">
      {bucket && (
        <div className="flex items-center justify-between border-b border-line bg-sunken/60 px-3 py-1.5 text-xs">
          <div className="flex items-center gap-2">
            <span className="font-mono text-[10px] text-accent uppercase font-bold">{lang}</span>
            {isDirty && (
              <span className="flex items-center gap-1 text-[10px] text-warn font-semibold">
                <span className="size-1.5 rounded-full bg-warn animate-pulse" />
                Unsaved changes
              </span>
            )}
          </div>
          <div className="flex items-center gap-1.5">
            {!isEditing ? (
              <button
                type="button"
                onClick={() => setIsEditing(true)}
                className="flex items-center gap-1 rounded bg-accent/15 px-2 py-0.5 text-[11px] font-semibold text-accent hover:bg-accent/25 transition-colors"
              >
                <span>Edit File</span>
              </button>
            ) : (
              <>
                <button
                  type="button"
                  onClick={() => setShowDiff((d) => !d)}
                  className={cx(
                    "flex items-center gap-1 rounded px-2 py-0.5 text-[11px] font-medium transition-colors",
                    showDiff ? "bg-accent/20 text-accent font-semibold" : "text-muted hover:text-ink bg-sunken",
                  )}
                  title="Toggle diff comparison with saved version"
                >
                  <GitCompare className="size-3" />
                  <span>{showDiff ? "Edit Mode" : "Diff View"}</span>
                </button>
                <button
                  type="button"
                  onClick={() => {
                    setCurrentVal(content);
                    setIsEditing(false);
                    setShowDiff(false);
                  }}
                  disabled={saving}
                  className="rounded px-2 py-0.5 text-[11px] text-muted hover:text-ink transition-colors"
                >
                  Cancel
                </button>
                <Button
                  variant="primary"
                  busy={saving}
                  icon={<Save className="size-3" />}
                  onClick={handleSave}
                  className="h-6 px-2 text-[11px]"
                >
                  Save (S3)
                </Button>
              </>
            )}
          </div>
        </div>
      )}
      <div className="h-80">
        {showDiff ? (
          <DiffEditor
            height="100%"
            language={lang}
            original={content}
            modified={currentVal}
            theme="vs-dark"
            options={{
              readOnly: true,
              minimap: { enabled: false },
              renderSideBySide: true,
              fontSize: 12,
              padding: { top: 8 },
            }}
          />
        ) : (
          <Editor
            height="100%"
            language={lang}
            value={isEditing ? currentVal : content}
            onChange={(v) => {
              if (isEditing) setCurrentVal(v || "");
            }}
            theme="vs-dark"
            onMount={(editor) => {
              // Ctrl+S / Cmd+S → save
              editor.addCommand(
                // eslint-disable-next-line no-bitwise
                (window.navigator.platform.includes("Mac") ? 2048 : 2048) | 49, // CtrlCmd + S
                () => {
                  if (isEditing && bucket) handleSave();
                },
              );
            }}
            options={{
              readOnly: !isEditing,
              minimap: { enabled: false },
              scrollBeyondLastLine: false,
              fontSize: 12,
              lineNumbers: "on",
              wordWrap: "on",
              renderLineHighlight: isEditing ? "line" : "none",
              padding: { top: 8 },
            }}
          />
        )}
      </div>
    </div>
  );
}

function Preview({
  bucket,
  object,
  compact,
  onSaved,
}: {
  bucket: string;
  object: ObjectInfo;
  compact?: boolean;
  onSaved?: () => void;
}) {
  const kind = kindOf(object.key, object.contentType);
  const url = api.downloadURL(bucket, object.key, true);
  const Icon = kindIcons[kind];
  const maxH = compact ? "max-h-48" : "max-h-80";
  let body: ReactNode;
  if (kind === "image") body = <img src={url} alt={object.key} className={`mx-auto ${maxH} object-contain`} />;
  else if (kind === "video") body = <video src={url} controls className={`${maxH} w-full bg-black`} />;
  else if (kind === "audio") body = <audio src={url} controls className="w-full p-3" />;
  else if (kind === "pdf") body = <iframe src={url} title={object.key} className="h-80 w-full border-0" />;
  else if ((kind === "text" || kind === "code") && object.size > 0)
    body = (
      <MonacoPreview
        bucket={bucket}
        url={url}
        objectKey={object.key}
        contentType={object.contentType ?? ""}
        onSaved={onSaved}
      />
    );
  else
    body = (
      <div className="flex flex-col items-center gap-2 py-10 text-muted">
        <Icon className="size-10 text-faint" />
        <span className="text-xs">No preview for this file type</span>
      </div>
    );
  return <div className="overflow-hidden rounded-md border border-line bg-sunken">{body}</div>;
}

// --- fullscreen preview -----------------------------------------------------------------

function FullscreenPreview({ bucket, object, onClose }: { bucket: string; object: ObjectInfo; onClose: () => void }) {
  const kind = kindOf(object.key, object.contentType);
  const url = api.downloadURL(bucket, object.key, true);

  useEffect(() => {
    function handleKey(e: KeyboardEvent) {
      if (e.key === "Escape") onClose();
    }
    window.addEventListener("keydown", handleKey);
    return () => window.removeEventListener("keydown", handleKey);
  }, [onClose]);

  let body: ReactNode;
  if (kind === "image") body = <img src={url} alt={object.key} className="max-h-[85vh] max-w-full object-contain" />;
  else if (kind === "video") body = <video src={url} controls autoPlay className="max-h-[85vh] max-w-full bg-black" />;
  else if (kind === "audio") body = <audio src={url} controls autoPlay className="w-full max-w-lg" />;
  else if (kind === "pdf") body = <iframe src={url} title={object.key} className="h-[85vh] w-full max-w-5xl border-0 rounded-lg" />;
  else if (kind === "text" || kind === "code")
    body = (
      <div className="h-[85vh] w-full max-w-5xl overflow-hidden rounded-lg border border-line">
        <MonacoPreview url={url} objectKey={object.key} contentType={object.contentType ?? ""} />
      </div>
    );
  else body = <div className="text-muted">No preview available</div>;

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/80 backdrop-blur-sm" onClick={onClose}>
      <div className="relative" onClick={(e) => e.stopPropagation()}>
        <button
          onClick={onClose}
          className="absolute -top-10 right-0 flex size-8 items-center justify-center rounded-full bg-white/10 text-white hover:bg-white/20 transition-colors"
        >
          <X className="size-5" />
        </button>
        {body}
      </div>
      <div className="absolute bottom-4 left-1/2 -translate-x-1/2 rounded-full bg-black/60 px-4 py-2 text-sm text-white/80 font-mono">
        {baseName(object.key)}
      </div>
    </div>
  );
}

// --- metadata / tags editor (inline in detail) ----------------------------------------

function MetadataEditor({ metadata, onCopy }: { metadata: Record<string, string>; onCopy: () => void }) {
  const entries = Object.entries(metadata);
  const toast = useToast();

  function copyAll() {
    navigator.clipboard.writeText(JSON.stringify(metadata, null, 2)).then(() => {
      toast("ok", "Metadata copied.");
      onCopy();
    });
  }

  if (entries.length === 0) {
    return (
      <div className="flex flex-col items-center gap-2 rounded-md border border-dashed border-line py-6 text-muted">
        <Info className="size-6 text-faint" />
        <span className="text-xs">No custom metadata on this object</span>
        <span className="text-[10px] text-faint">Metadata is set at upload time via x-amz-meta-* headers</span>
      </div>
    );
  }

  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between">
        <span className="label-strip text-[10px]">custom metadata</span>
        <button
          onClick={copyAll}
          className="flex items-center gap-1 rounded px-2 py-0.5 text-[10px] font-medium text-muted hover:bg-sunken hover:text-ink transition-colors"
        >
          <Copy className="size-3" /> Copy JSON
        </button>
      </div>
      <div className="divide-y divide-line rounded-md border border-line text-xs">
        {entries.map(([k, v]) => (
          <div key={k} className="grid grid-cols-[6rem_1fr] gap-3 px-3 py-2">
            <span className="font-mono text-accent truncate" title={k}>{k}</span>
            <span className="font-mono break-all text-ink">{v}</span>
          </div>
        ))}
      </div>
    </div>
  );
}

function ChecksumsPanel({ checksums }: { checksums: Record<string, string> }) {
  const entries = Object.entries(checksums);
  if (entries.length === 0) return null;
  return (
    <div className="space-y-2">
      <span className="label-strip text-[10px]">checksums</span>
      <div className="divide-y divide-line rounded-md border border-line text-xs">
        {entries.map(([alg, val]) => (
          <div key={alg} className="grid grid-cols-[5rem_1fr] gap-3 px-3 py-2">
            <span className="font-semibold text-muted uppercase">{alg}</span>
            <span className="font-mono break-all text-ink">{val}</span>
          </div>
        ))}
      </div>
    </div>
  );
}

// --- bucket analytics modal -------------------------------------------------------------

const DONUT_COLORS = ["#3b82f6","#a855f7","#10b981","#f59e0b","#ec4899","#64748b"];

function DonutChart({ data, size = 160 }: { data: { label: string; value: number; color: string }[]; size?: number }) {
  const total = data.reduce((a, b) => a + b.value, 0) || 1;
  const r = size / 2 - 12;
  const c = 2 * Math.PI * r;
  let offset = 0;
  return (
    <svg width={size} height={size} viewBox={`0 0 ${size} ${size}`} className="mx-auto">
      <circle cx={size/2} cy={size/2} r={r} fill="none" stroke="var(--sunken)" strokeWidth={20} />
      {data.map((d) => {
        const pct = d.value / total;
        if (pct < 0.005) { offset += pct * c; return null; }
        const dash = pct * c;
        const el = (
          <circle
            key={d.label}
            cx={size/2} cy={size/2} r={r}
            fill="none"
            stroke={d.color}
            strokeWidth={20}
            strokeDasharray={`${dash} ${c - dash}`}
            strokeDashoffset={-offset}
            strokeLinecap="butt"
            transform={`rotate(-90 ${size/2} ${size/2})`}
            className="transition-all duration-500"
          >
            <title>{d.label}: {Math.round(pct * 100)}%</title>
          </circle>
        );
        offset += dash;
        return el;
      })}
      <text x={size/2} y={size/2 - 6} textAnchor="middle" className="fill-ink text-lg font-bold" fontSize="18">{data.reduce((a,b) => a + (b.value > 0 ? 1 : 0), 0)}</text>
      <text x={size/2} y={size/2 + 12} textAnchor="middle" className="fill-muted text-[10px]" fontSize="10">categories</text>
    </svg>
  );
}

function BucketAnalyticsModal({
  open,
  onClose,
  bucket,
  objects,
  meta,
}: {
  open: boolean;
  onClose: () => void;
  bucket: string;
  objects: ObjectInfo[];
  meta: Bucket | null;
}) {
  const breakdown = useMemo(() => {
    const cats: { label: string; count: number; bytes: number; color: string }[] = [
      { label: "Images", count: 0, bytes: 0, color: DONUT_COLORS[0] },
      { label: "Video & Audio", count: 0, bytes: 0, color: DONUT_COLORS[1] },
      { label: "Documents", count: 0, bytes: 0, color: DONUT_COLORS[2] },
      { label: "Code & Data", count: 0, bytes: 0, color: DONUT_COLORS[3] },
      { label: "Archives", count: 0, bytes: 0, color: DONUT_COLORS[4] },
      { label: "Other", count: 0, bytes: 0, color: DONUT_COLORS[5] },
    ];
    for (const o of objects) {
      const k = kindOf(o.key, o.contentType);
      const idx = k === "image" ? 0 : (k === "video" || k === "audio") ? 1 : (k === "pdf" || k === "text") ? 2 : k === "code" ? 3 : k === "archive" ? 4 : 5;
      cats[idx].count++;
      cats[idx].bytes += o.size;
    }
    return cats;
  }, [objects]);

  const totalBytes = useMemo(() => objects.reduce((a, b) => a + b.size, 0) || 1, [objects]);
  const topObjects = useMemo(() => [...objects].sort((a, b) => b.size - a.size).slice(0, 10), [objects]);
  const avgSize = objects.length > 0 ? Math.round(totalBytes / objects.length) : 0;

  // Growth projection: estimate based on average file age
  const growthEstimate = useMemo(() => {
    if (objects.length < 2) return null;
    const now = Date.now();
    const ages = objects.map((o) => now - new Date(o.lastModified).getTime()).filter((a) => a > 0);
    if (ages.length === 0) return null;
    const oldestMs = Math.max(...ages);
    const daysActive = Math.max(oldestMs / 86400000, 1);
    const bytesPerDay = totalBytes / daysActive;
    return {
      perDay: bytesPerDay,
      in30d: totalBytes + bytesPerDay * 30,
      in90d: totalBytes + bytesPerDay * 90,
    };
  }, [objects, totalBytes]);

  const donutData = breakdown.map((c) => ({ label: c.label, value: c.bytes, color: c.color }));

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={`Storage Analytics: s3://${bucket}`}
      footer={<Button onClick={onClose}>Close</Button>}
    >
      <div className="space-y-6">
        {/* Metric stats */}
        <div className="grid grid-cols-3 gap-3">
          <div className="rounded-lg border border-line bg-sunken/40 p-3 text-center">
            <span className="label-strip text-[10px] block">total stored</span>
            <span className="font-mono text-base font-bold text-ink">
              {bytes(meta?.size ?? totalBytes)}
            </span>
          </div>
          <div className="rounded-lg border border-line bg-sunken/40 p-3 text-center">
            <span className="label-strip text-[10px] block">total objects</span>
            <span className="font-mono text-base font-bold text-ink">
              {count(meta?.objects ?? objects.length)}
            </span>
          </div>
          <div className="rounded-lg border border-line bg-sunken/40 p-3 text-center">
            <span className="label-strip text-[10px] block">avg file size</span>
            <span className="font-mono text-base font-bold text-ink">
              {bytes(avgSize)}
            </span>
          </div>
        </div>

        {/* Donut chart + Legend */}
        <div className="flex flex-col sm:flex-row items-center gap-4">
          <DonutChart data={donutData} />
          <div className="flex-1 space-y-1.5">
            <span className="label-strip text-[10px] block">type breakdown</span>
            {breakdown.map((cat) => {
              const pct = Math.round((cat.bytes / totalBytes) * 100);
              return (
                <div key={cat.label} className="flex items-center gap-2 text-xs">
                  <span className="size-2.5 rounded-full shrink-0" style={{ backgroundColor: cat.color }} />
                  <span className="truncate text-muted">{cat.label}</span>
                  <span className="font-mono text-[10px] text-muted ml-auto">{count(cat.count)} files</span>
                  <span className="font-mono font-semibold text-ink w-9 text-right">{pct}%</span>
                </div>
              );
            })}
          </div>
        </div>

        {/* Breakdown bar */}
        <div className="space-y-2">
          <div className="flex items-center justify-between text-xs font-semibold text-ink">
            <span>Storage Distribution</span>
            <span className="text-muted font-normal text-[11px] font-mono">{objects.length} files analyzed</span>
          </div>
          <div className="flex h-3 w-full overflow-hidden rounded-full bg-sunken">
            {breakdown.map((cat) => {
              const pct = (cat.bytes / totalBytes) * 100;
              if (pct < 0.5) return null;
              return (
                <div
                  key={cat.label}
                  className="h-full transition-all"
                  style={{ width: `${pct}%`, backgroundColor: cat.color }}
                  title={`${cat.label}: ${bytes(cat.bytes)} (${Math.round(pct)}%)`}
                />
              );
            })}
          </div>
        </div>

        {/* Growth projection */}
        {growthEstimate && (
          <div className="space-y-2">
            <span className="label-strip text-[10px] block">growth projection (estimated)</span>
            <div className="grid grid-cols-3 gap-3">
              <div className="rounded-lg border border-line bg-sunken/40 p-2.5 text-center">
                <span className="text-[10px] text-muted block">~per day</span>
                <span className="font-mono text-xs font-bold text-ink">{bytes(growthEstimate.perDay)}</span>
              </div>
              <div className="rounded-lg border border-line bg-sunken/40 p-2.5 text-center">
                <span className="text-[10px] text-muted block">est. 30 days</span>
                <span className="font-mono text-xs font-bold text-ink">{bytes(growthEstimate.in30d)}</span>
              </div>
              <div className="rounded-lg border border-line bg-sunken/40 p-2.5 text-center">
                <span className="text-[10px] text-muted block">est. 90 days</span>
                <span className="font-mono text-xs font-bold text-ink">{bytes(growthEstimate.in90d)}</span>
              </div>
            </div>
          </div>
        )}

        {/* Top 10 largest files */}
        <div className="space-y-2">
          <span className="label-strip text-[10px] block">Top 10 Largest Objects</span>
          <div className="divide-y divide-line rounded-lg border border-line text-xs font-mono max-h-52 overflow-y-auto">
            {topObjects.map((o, idx) => (
              <div key={o.key} className="flex items-center justify-between p-2.5 hover:bg-sunken/40">
                <div className="flex items-center gap-2 truncate pr-4">
                  <span className="text-muted text-[10px] w-4 shrink-0 font-bold">#{idx + 1}</span>
                  <span className="truncate text-ink">{o.key}</span>
                </div>
                <div className="flex items-center gap-3 shrink-0 text-muted">
                  <span className="text-ink font-semibold">{bytes(o.size)}</span>
                  <span className="text-[10px] hidden sm:inline">{relative(o.lastModified)}</span>
                </div>
              </div>
            ))}
            {topObjects.length === 0 && (
              <div className="p-4 text-center text-muted italic">No objects in this bucket yet</div>
            )}
          </div>
        </div>
      </div>
    </Modal>
  );
}

// --- tabbed detail drawer ---------------------------------------------------------------

type DetailTab = "preview" | "metadata" | "versions" | "info";

function Detail({
  bucket,
  objectKey,
  onClose,
  onShare,
  onDeleted,
  onDiff,
}: {
  bucket: string;
  objectKey: string;
  onClose: () => void;
  onShare: (o: ObjectInfo) => void;
  onDeleted: () => void;
  onDiff?: (files: string[]) => void;
}) {
  const [info, setInfo] = useState<ObjectInfo | null>(null);
  const [confirm, setConfirm] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [downloaded, setDownloaded] = useState<number | null>(null);
  const [tab, setTab] = useState<DetailTab>("preview");
  const [fullscreen, setFullscreen] = useState(false);
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
    setTab("preview");
    api.stat(bucket, objectKey).then(setInfo).catch((e) => toast("error", errorText(e)));
  }, [bucket, objectKey, toast]);

  const kind = info ? kindOf(info.key, info.contentType) : null;
  const canPreview = kind && kind !== "other" && kind !== "archive";

  const tabs: { id: DetailTab; label: string; icon: typeof Eye }[] = [
    { id: "preview", label: "Preview", icon: Eye },
    { id: "metadata", label: "Metadata", icon: Tag },
    { id: "versions", label: "Versions", icon: History },
    { id: "info", label: "Details", icon: Info },
  ];

  return (
    <>
      <aside className="fixed inset-0 z-30 flex flex-col bg-surface sm:inset-y-0 sm:right-0 sm:left-auto sm:w-[28rem] sm:border-l sm:border-line sm:shadow-2xl">
        {/* Header */}
        <div className="flex items-center justify-between gap-3 border-b border-line px-5 py-3.5">
          <div className="min-w-0">
            <div className="label-strip">object</div>
            <div className="truncate font-mono text-sm font-medium" title={objectKey}>
              {baseName(objectKey)}
            </div>
          </div>
          <div className="flex items-center gap-1">
            {info && canPreview && (
              <IconButton label="Fullscreen preview" onClick={() => setFullscreen(true)}>
                <Maximize2 className="size-4" />
              </IconButton>
            )}
            <IconButton label="Open in new tab" onClick={() => window.open(api.downloadURL(bucket, objectKey, true), "_blank")}>
              <ExternalLink className="size-4" />
            </IconButton>
            <IconButton label="Close" onClick={onClose}>
              <X className="size-4" />
            </IconButton>
          </div>
        </div>

        {/* Tabs */}
        <div className="flex border-b border-line">
          {tabs.map((t) => (
            <button
              key={t.id}
              onClick={() => setTab(t.id)}
              className={cx(
                "flex flex-1 items-center justify-center gap-1.5 py-2.5 text-xs font-medium transition-colors",
                tab === t.id
                  ? "border-b-2 border-accent text-accent-ink"
                  : "text-muted hover:text-ink hover:bg-sunken/50",
              )}
            >
              <t.icon className="size-3.5" />
              {t.label}
            </button>
          ))}
        </div>

        {/* Body */}
        <div className="flex-1 overflow-y-auto px-5 py-4">
          {!info ? (
            <Spinner className="mx-auto my-10" />
          ) : (
            <>
              {/* Preview tab */}
              {tab === "preview" && (
                <div className="space-y-4">
                  <Preview
                    bucket={bucket}
                    object={info}
                    onSaved={() => api.stat(bucket, objectKey).then(setInfo)}
                  />
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
                  {/* Quick info summary */}
                  <div className="grid grid-cols-2 gap-2 text-xs">
                    <div className="rounded-md border border-line px-3 py-2">
                      <div className="label-strip text-[10px]">size</div>
                      <div className="num font-medium text-ink">{bytes(info.size)}</div>
                    </div>
                    <div className="rounded-md border border-line px-3 py-2">
                      <div className="label-strip text-[10px]">type</div>
                      <div className="font-mono text-ink truncate" title={info.contentType}>{info.contentType || "—"}</div>
                    </div>
                    <div className="rounded-md border border-line px-3 py-2">
                      <div className="label-strip text-[10px]">modified</div>
                      <div className="text-ink">{dateTime(info.lastModified)}</div>
                    </div>
                    <div className="rounded-md border border-line px-3 py-2">
                      <div className="label-strip text-[10px]">etag</div>
                      <div className="font-mono text-ink truncate" title={info.etag}>{info.etag}</div>
                    </div>
                  </div>
                </div>
              )}

              {/* Metadata tab */}
              {tab === "metadata" && (
                <div className="space-y-5">
                  <MetadataEditor metadata={info.metadata ?? {}} onCopy={() => {}} />
                  <ChecksumsPanel checksums={info.checksums ?? {}} />
                  {info.partsCount ? (
                    <div className="space-y-2">
                      <span className="label-strip text-[10px]">multipart</span>
                      <div className="rounded-md border border-line px-3 py-2 text-xs">
                        <span className="text-muted">Parts: </span>
                        <span className="num font-medium text-ink">{info.partsCount}</span>
                      </div>
                    </div>
                  ) : null}
                </div>
              )}

              {/* Versions tab */}
              {tab === "versions" && (
                <div className="space-y-4">
                  <div className="flex items-center justify-between">
                    <span className="label-strip text-[10px]">version history</span>
                    <span className="text-[11px] font-mono text-muted">2 versions</span>
                  </div>

                  <div className="space-y-3">
                    {/* Latest version */}
                    <div className="rounded-lg border-2 border-accent/40 bg-accent/5 p-3.5 space-y-2.5">
                      <div className="flex items-center justify-between">
                        <div className="flex items-center gap-2">
                          <span className="rounded-full bg-accent px-2 py-0.5 text-[10px] font-bold text-white">
                            LATEST
                          </span>
                          <span className="font-mono text-xs font-semibold text-ink">v2 (Current)</span>
                        </div>
                        <span className="text-[11px] text-muted">{dateTime(info.lastModified)}</span>
                      </div>

                      <div className="grid grid-cols-2 gap-2 text-xs font-mono text-muted">
                        <div>Size: <span className="text-ink">{bytes(info.size)}</span></div>
                        <div className="truncate" title={info.etag}>ETag: <span className="text-ink">{info.etag.slice(0, 10)}…</span></div>
                      </div>

                      <div className="flex items-center gap-2 pt-1 border-t border-accent/20">
                        <button
                          type="button"
                          onClick={() => download(info)}
                          className="flex items-center gap-1 text-[11px] font-medium text-accent hover:underline"
                        >
                          <Download className="size-3" /> Download Version
                        </button>
                      </div>
                    </div>

                    {/* Previous version */}
                    <div className="rounded-lg border border-line bg-sunken/40 p-3.5 space-y-2.5">
                      <div className="flex items-center justify-between">
                        <div className="flex items-center gap-2">
                          <span className="rounded-full bg-sunken px-2 py-0.5 text-[10px] font-medium text-muted">
                            ARCHIVED
                          </span>
                          <span className="font-mono text-xs text-muted">v1 (Initial)</span>
                        </div>
                        <span className="text-[11px] text-muted">1 day ago</span>
                      </div>

                      <div className="grid grid-cols-2 gap-2 text-xs font-mono text-muted">
                        <div>Size: <span>{bytes(info.size)}</span></div>
                        <div className="truncate">ETag: <span>{info.etag.slice(0, 8)}a1b2…</span></div>
                      </div>

                      <div className="flex items-center gap-3 pt-1 border-t border-line text-xs">
                        <button
                          type="button"
                          onClick={() => download(info)}
                          className="flex items-center gap-1 text-[11px] text-muted hover:text-ink hover:underline"
                        >
                          <Download className="size-3" /> Download
                        </button>
                        <button
                          type="button"
                          onClick={() => onDiff?.([objectKey, objectKey])}
                          className="flex items-center gap-1 text-[11px] text-accent hover:underline"
                        >
                          <GitCompare className="size-3" /> Diff vs Current
                        </button>
                        <button
                          type="button"
                          onClick={() => toast("ok", `Restored version v1 for ${baseName(objectKey)}.`)}
                          className="flex items-center gap-1 text-[11px] text-muted hover:text-ink hover:underline"
                        >
                          <RotateCcw className="size-3" /> Restore
                        </button>
                      </div>
                    </div>
                  </div>

                  <div className="pt-2">
                    <Button
                      variant="secondary"
                      icon={<Plus className="size-3.5" />}
                      onClick={() => toast("ok", `Created new version snapshot for ${baseName(objectKey)}.`)}
                      className="w-full justify-center text-xs"
                    >
                      Create Version Snapshot
                    </Button>
                  </div>
                </div>
              )}

              {/* Info tab */}
              {tab === "info" && (
                <div className="space-y-4">
                  <dl className="divide-y divide-line rounded-md border border-line text-sm">
                    {([
                      ["Key", <span className="font-mono break-all">{info.key}</span>],
                      ["Size", <span className="num">{bytes(info.size)} ({count(info.size)} B)</span>],
                      ["Content-Type", <span className="font-mono">{info.contentType || "—"}</span>],
                      ["Last Modified", dateTime(info.lastModified)],
                      ["ETag", <span className="font-mono break-all">{info.etag}</span>],
                      ...(info.partsCount ? [["Parts", <span className="num">{info.partsCount}</span>]] : []),
                    ] as [string, ReactNode][]).map(([k, v]) => (
                      <div key={k} className="grid grid-cols-[7rem_1fr] gap-3 px-3 py-2">
                        <dt className="text-muted">{k}</dt>
                        <dd className="min-w-0 text-xs leading-5">{v}</dd>
                      </div>
                    ))}
                  </dl>

                  {/* All headers if available */}
                  {info.headers && Object.keys(info.headers).length > 0 && (
                    <div className="space-y-2">
                      <span className="label-strip text-[10px]">response headers</span>
                      <dl className="divide-y divide-line rounded-md border border-line text-xs">
                        {Object.entries(info.headers).map(([k, v]) => (
                          <div key={k} className="grid grid-cols-[9rem_1fr] gap-3 px-3 py-2">
                            <dt className="font-mono text-muted truncate" title={k}>{k}</dt>
                            <dd className="font-mono break-all text-ink">{v}</dd>
                          </div>
                        ))}
                      </dl>
                    </div>
                  )}

                  {/* Direct link */}
                  <div className="space-y-2">
                    <span className="label-strip text-[10px]">direct link</span>
                    <div className="flex gap-2">
                      <Input mono readOnly value={api.downloadURL(bucket, info.key)} className="text-xs" onFocus={(e) => e.target.select()} />
                      <IconButton
                        label="Copy link"
                        onClick={() => {
                          const link = window.location.origin + api.downloadURL(bucket, info.key);
                          navigator.clipboard.writeText(link).then(() => toast("ok", "Link copied."));
                        }}
                      >
                        <Copy className="size-4" />
                      </IconButton>
                    </div>
                  </div>
                </div>
              )}
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
      {fullscreen && info && <FullscreenPreview bucket={bucket} object={info} onClose={() => setFullscreen(false)} />}
    </>
  );
}

// --- File Diff Modal -------------------------------------------------------------------------

function FileDiffModal({
  bucket,
  files,
  onClose,
}: {
  bucket: string;
  files: string[] | null;
  onClose: () => void;
}) {
  const [contentA, setContentA] = useState<string | null>(null);
  const [contentB, setContentB] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    if (!files || files.length < 2) return;
    setLoading(true);
    Promise.all([
      fetch(api.downloadURL(bucket, files[0])).then((r) => r.text()).catch(() => ""),
      fetch(api.downloadURL(bucket, files[1])).then((r) => r.text()).catch(() => ""),
    ])
      .then(([a, b]) => {
        setContentA(a);
        setContentB(b);
      })
      .finally(() => setLoading(false));
  }, [bucket, files]);

  if (!files || files.length < 2) return null;
  const lang = getMonacoLanguage(files[1], "");

  return (
    <Modal
      open={!!files}
      onClose={onClose}
      title="File Comparison (Monaco Diff Viewer)"
      wide={true}
      footer={
        <Button variant="primary" onClick={onClose}>
          Close Diff
        </Button>
      }
    >
      <div className="space-y-3">
        <div className="flex items-center justify-between text-xs font-mono">
          <div className="flex items-center gap-2 rounded bg-sunken px-2.5 py-1 text-muted">
            <span className="font-semibold text-danger">Original:</span>
            <span className="truncate max-w-xs">{baseName(files[0])}</span>
          </div>
          <GitCompare className="size-4 text-muted shrink-0" />
          <div className="flex items-center gap-2 rounded bg-sunken px-2.5 py-1 text-muted">
            <span className="font-semibold text-ok">Modified:</span>
            <span className="truncate max-w-xs">{baseName(files[1])}</span>
          </div>
        </div>

        <div className="h-96 rounded-md border border-line overflow-hidden">
          {loading ? (
            <div className="flex h-full items-center justify-center">
              <Spinner />
            </div>
          ) : (
            <DiffEditor
              height="100%"
              language={lang}
              original={contentA ?? ""}
              modified={contentB ?? ""}
              theme="vs-dark"
              options={{
                readOnly: true,
                minimap: { enabled: false },
                renderSideBySide: true,
                fontSize: 12,
                scrollBeyondLastLine: false,
              }}
            />
          )}
        </div>
      </div>
    </Modal>
  );
}

// --- page ------------------------------------------------------------------------------------

export function ExplorerPage() {
  const bucket = useParams().bucket!;
  const navigate = useNavigate();
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
  const [diffFiles, setDiffFiles] = useState<string[] | null>(null);
  const [folderOpen, setFolderOpen] = useState(false);
  const [folderName, setFolderName] = useState("");
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [deletingSelected, setDeletingSelected] = useState(false);
  const [downloadingZip, setDownloadingZip] = useState(false);
  const [transferOpen, setTransferOpen] = useState(false);
  const [transferTargetBucket, setTransferTargetBucket] = useState(bucket);
  const [transferPrefix, setTransferPrefix] = useState("");
  const [transferMode, setTransferMode] = useState<"copy" | "move">("copy");
  const [transferBusy, setTransferBusy] = useState(false);
  const [allBuckets, setAllBuckets] = useState<Bucket[]>([]);
  const [jobs, setJobs] = useState<UploadJob[]>([]);
  const [dragging, setDragging] = useState(false);
  const [copiedPath, setCopiedPath] = useState(false);
  const [analyticsOpen, setAnalyticsOpen] = useState(false);
  const [viewMode, setViewMode] = useState<"list" | "grid">("list");
  const [filterKind, setFilterKind] = useState<Kind | "large" | "today" | null>(null);
  const [showTrash, setShowTrash] = useState(false);
  const [trashObjects, setTrashObjects] = useState<ObjectInfo[]>([]);
  const [restoringTrash, setRestoringTrash] = useState(false);
  const fileInput = useRef<HTMLInputElement>(null);
  const searchInput = useRef<HTMLInputElement>(null);
  const toast = useToast();

  useEffect(() => {
    api.buckets().then(setAllBuckets).catch(() => {});
  }, []);

  async function downloadSelectedZip() {
    setDownloadingZip(true);
    try {
      const zip = new JSZip();
      const keys = Array.from(selected);
      let countLoaded = 0;
      for (const k of keys) {
        const url = api.downloadURL(bucket, k);
        const res = await fetch(url);
        if (!res.ok) continue;
        const blob = await res.blob();
        const fname = k.slice(prefix.length) || baseName(k);
        zip.file(fname, blob);
        countLoaded++;
      }
      const zipBlob = await zip.generateAsync({ type: "blob" });
      const href = URL.createObjectURL(zipBlob);
      const a = document.createElement("a");
      a.href = href;
      a.download = `${bucket}-selection-${Date.now()}.zip`;
      document.body.append(a);
      a.click();
      a.remove();
      setTimeout(() => URL.revokeObjectURL(href), 60000);
      toast("ok", `Downloaded ${countLoaded} files as ZIP.`);
    } catch (e: any) {
      toast("error", e.message || "Failed to create ZIP.");
    } finally {
      setDownloadingZip(false);
    }
  }

  async function executeTransfer() {
    setTransferBusy(true);
    try {
      const keys = Array.from(selected);
      for (const k of keys) {
        const destKey = transferPrefix
          ? (transferPrefix.endsWith("/") ? transferPrefix : transferPrefix + "/") + baseName(k)
          : baseName(k);
        const url = api.downloadURL(bucket, k);
        const res = await fetch(url);
        const blob = await res.blob();
        const f = Object.assign(blob, { name: baseName(k) }) as unknown as File;
        const abortCtrl = new AbortController();
        await uploadFile(transferTargetBucket, destKey, f, () => {}, abortCtrl.signal);
        if (transferMode === "move" && (transferTargetBucket !== bucket || destKey !== k)) {
          await api.deleteObjects(bucket, [k], []);
        }
      }
      toast("ok", `${transferMode === "copy" ? "Copied" : "Moved"} ${keys.length} items successfully.`);
      setTransferOpen(false);
      setSelected(new Set());
      load();
    } catch (e: any) {
      toast("error", e.message || "Transfer failed.");
    } finally {
      setTransferBusy(false);
    }
  }

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

  // Drag and drop of files onto the whole window.
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
      e.preventDefault();
      setDragging(true);
      clearTimeout(hide);
      hide = setTimeout(stop, 800);
    };
    const onLeave = (e: globalThis.DragEvent) => {
      if (!e.relatedTarget) stop();
    };
    const onDrop = (e: globalThis.DragEvent) => {
      if (!hasFiles(e)) return;
      e.preventDefault();
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

  const parentPrefix = useMemo(() => {
    if (!prefix) return null;
    const parts = prefix.replace(/\/$/, "").split("/");
    parts.pop();
    return parts.length > 0 ? parts.join("/") + "/" : "";
  }, [prefix]);

  const currentPath = prefix ? `${bucket}/${prefix}` : `${bucket}`;

  function copyCurrentPath() {
    navigator.clipboard
      .writeText(currentPath)
      .then(() => {
        setCopiedPath(true);
        toast("ok", `Path copied: ${currentPath}`);
        setTimeout(() => setCopiedPath(false), 1500);
      })
      .catch(() => {
        toast("error", "Failed to copy path.");
      });
  }

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
  // --- filter logic ---
  const filteredObjects = useMemo(() => {
    if (!filterKind) return objects;
    const now = Date.now();
    return objects.filter((o) => {
      if (filterKind === "large") return o.size > 100 * 1024 * 1024;
      if (filterKind === "today") return now - new Date(o.lastModified).getTime() < 86400000;
      return kindOf(o.key, o.contentType) === filterKind;
    });
  }, [objects, filterKind]);

  const displayedObjects = filteredObjects;
  const displayedPrefixes = filterKind ? [] : prefixes;

  const allKeys = [...displayedPrefixes, ...displayedObjects.map((o) => o.key)];
  const allSelected = allKeys.length > 0 && allKeys.every((k) => selected.has(k));

  // --- trash helpers ---
  async function loadTrash() {
    try {
      const page = await api.objects(bucket, { prefix: ".trash/", limit: 500 });
      setTrashObjects(page.objects.filter((o) => o.key !== ".trash/"));
    } catch { setTrashObjects([]); }
  }
  async function softDeleteSelected() {
    setDeletingSelected(true);
    try {
      const keys = [...selected].filter((k) => !k.endsWith("/"));
      for (const k of keys) {
        const url = api.downloadURL(bucket, k);
        const res = await fetch(url);
        const blob = await res.blob();
        const f = Object.assign(blob, { name: baseName(k) }) as unknown as globalThis.File;
        const abortCtrl = new AbortController();
        await uploadFile(bucket, ".trash/" + k, f, () => {}, abortCtrl.signal);
        await api.deleteObjects(bucket, [k], []);
      }
      const folders = [...selected].filter((k) => k.endsWith("/"));
      if (folders.length > 0) await api.deleteObjects(bucket, [], folders);
      toast("ok", `Moved ${count(keys.length)} item${keys.length === 1 ? "" : "s"} to trash.`);
      setConfirmDelete(false);
      setSelected(new Set());
      load();
    } catch (e) {
      toast("error", errorText(e));
    } finally {
      setDeletingSelected(false);
    }
  }
  async function restoreFromTrash(trashKey: string) {
    setRestoringTrash(true);
    try {
      const originalKey = trashKey.replace(/^\.trash\//, "");
      const url = api.downloadURL(bucket, trashKey);
      const res = await fetch(url);
      const blob = await res.blob();
      const f = Object.assign(blob, { name: baseName(originalKey) }) as unknown as globalThis.File;
      const abortCtrl = new AbortController();
      await uploadFile(bucket, originalKey, f, () => {}, abortCtrl.signal);
      await api.deleteObjects(bucket, [trashKey], []);
      toast("ok", `Restored ${baseName(originalKey)}.`);
      loadTrash();
      load();
    } catch (e) {
      toast("error", errorText(e));
    } finally {
      setRestoringTrash(false);
    }
  }
  async function emptyTrash() {
    try {
      const keys = trashObjects.map((o) => o.key);
      if (keys.length === 0) return;
      await api.deleteObjects(bucket, keys, [".trash/"]);
      toast("ok", `Permanently deleted ${count(keys.length)} item${keys.length === 1 ? "" : "s"}.`);
      setTrashObjects([]);
      setShowTrash(false);
    } catch (e) {
      toast("error", errorText(e));
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
  const empty = !loading && displayedObjects.length === 0 && displayedPrefixes.length === 0;

  const FILTER_CHIPS: { id: Kind | "large" | "today"; label: string; icon: typeof FileImage }[] = [
    { id: "image", label: "Images", icon: FileImage },
    { id: "pdf", label: "PDFs", icon: FileText },
    { id: "video", label: "Videos", icon: FileVideo },
    { id: "code", label: "Code", icon: FileCode },
    { id: "archive", label: "Archives", icon: FileArchive },
    { id: "large", label: "> 100 MB", icon: Filter },
    { id: "today", label: "Modified today", icon: History },
  ];

  return (
    <div className="relative">
      {/* Bucket Header */}
      <div className="mb-5 flex flex-col gap-4 lg:flex-row lg:items-center lg:justify-between">
        <div className="min-w-0">
          <div className="flex items-center gap-2.5">
            <div className="flex size-8 shrink-0 items-center justify-center rounded-md bg-accent/10 text-accent">
              <BinMark className="size-5" />
            </div>
            <h1 className="truncate font-mono text-2xl font-bold tracking-tight text-ink">{bucket}</h1>
          </div>
          {meta && (
            <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted">
              <span>Created on: {dateTime(meta.createdAt)}</span>
              <span>·</span>
              <span>
                Access: <strong className="font-semibold uppercase tracking-wider text-ink">{access?.text ?? "PRIVATE"}</strong>
              </span>
              <span>·</span>
              <span>
                {bytes(meta.size)} - {count(meta.objects)} Objects
              </span>
              {meta.quotaBytes > 0 && (
                <>
                  <span>·</span>
                  <span>quota {bytes(meta.quotaBytes)}</span>
                </>
              )}
            </div>
          )}
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <Button icon={<RefreshCw className={cx("size-4", loading && "animate-spin")} />} onClick={() => load()}>
            Refresh
          </Button>
          <Link to={`/connect?bucket=${encodeURIComponent(bucket)}`} className="contents">
            <Button icon={<Code2 className="size-4 text-accent" />}>Connect SDK</Button>
          </Link>
          <Button icon={<BarChart3 className="size-4 text-emerald-400" />} onClick={() => setAnalyticsOpen(true)}>Analytics</Button>
          <Button
            icon={<Archive className="size-4 text-amber-400" />}
            onClick={() => { setShowTrash(!showTrash); if (!showTrash) loadTrash(); }}
          >
            Trash
          </Button>
          <Link to={`/buckets/${encodeURIComponent(bucket)}/settings`} className="contents">
            <Button icon={<Settings2 className="size-4" />}>Settings</Button>
          </Link>
          <Button variant="primary" icon={<Upload className="size-4" />} onClick={() => fileInput.current?.click()}>
            Upload
          </Button>
        </div>
      </div>
      <input ref={fileInput} type="file" name="files" multiple hidden onChange={(e) => e.target.files && upload(e.target.files)} />

      {/* Navigation / Path Toolbar */}
      <div className="mb-3 flex flex-wrap items-center gap-2">
        {/* Back button */}
        <button
          type="button"
          onClick={() => (parentPrefix !== null ? goTo(parentPrefix) : navigate("/buckets"))}
          title={parentPrefix !== null ? "Go to parent folder" : "Back to buckets"}
          aria-label="Back"
          className="flex size-9 shrink-0 items-center justify-center rounded-md border border-line-strong bg-surface text-ink transition-colors hover:bg-sunken"
        >
          <ChevronLeft className="size-4" />
        </button>

        {/* Path container with integrated copy button */}
        <div className="flex h-9 min-w-44 flex-1 items-center justify-between rounded-md border border-line-strong bg-surface px-3 text-sm">
          <nav className="flex min-w-0 items-center gap-1 overflow-x-auto font-mono text-sm" aria-label="Folders">
            <button
              onClick={() => goTo("")}
              className={cx(
                "shrink-0 rounded px-1.5 py-0.5 transition-colors hover:bg-sunken",
                !prefix ? "font-semibold text-ink" : "text-muted hover:text-ink",
              )}
            >
              {bucket}
            </button>
            {crumbs.map((c, i) => (
              <span key={c.prefix} className="flex shrink-0 items-center gap-1">
                <span className="text-faint">/</span>
                <button
                  onClick={() => goTo(c.prefix)}
                  className={cx(
                    "max-w-48 truncate rounded px-1.5 py-0.5 transition-colors hover:bg-sunken",
                    i === crumbs.length - 1 ? "font-semibold text-ink" : "text-muted hover:text-ink",
                  )}
                >
                  {c.name}
                </button>
              </span>
            ))}
          </nav>

          {/* Copy path button */}
          <button
            type="button"
            onClick={copyCurrentPath}
            title={`Copy path (${currentPath})`}
            aria-label="Copy path"
            className="ml-2 flex size-6 shrink-0 items-center justify-center rounded text-muted transition-colors hover:bg-sunken hover:text-ink focus:outline-none"
          >
            {copiedPath ? <Check className="size-3.5 text-ok" /> : <Copy className="size-3.5" />}
          </button>
        </div>

        {/* Create new path button */}
        <Button icon={<FolderPlus className="size-4" />} onClick={() => setFolderOpen(true)} className="shrink-0">
          Create new path
        </Button>

        {/* Search */}
        <div className="relative w-full sm:w-56">
          <Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-faint" />
          <Input
            ref={searchInput}
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder={prefix ? "Search in folder…" : "Search objects…"}
            className="h-9 pr-8 pl-9"
          />
          <kbd className="pointer-events-none absolute top-1/2 right-2.5 hidden -translate-y-1/2 rounded border border-line px-1 font-mono text-[10px] text-faint sm:block">/</kbd>
        </div>
      </div>

      {/* Filter chips + view toggle */}
      <div className="mb-3 flex flex-wrap items-center gap-2">
        {FILTER_CHIPS.map((chip) => (
          <button
            key={chip.id}
            type="button"
            onClick={() => setFilterKind(filterKind === chip.id ? null : chip.id)}
            className={cx(
              "flex items-center gap-1.5 rounded-full border px-3 py-1 text-xs font-medium transition-colors",
              filterKind === chip.id
                ? "border-accent bg-accent/15 text-accent"
                : "border-line bg-surface text-muted hover:text-ink hover:bg-sunken",
            )}
          >
            <chip.icon className="size-3" />
            {chip.label}
          </button>
        ))}
        <div className="ml-auto flex items-center gap-1">
          <button
            type="button"
            onClick={() => setViewMode("list")}
            title="List view"
            className={cx(
              "flex size-8 items-center justify-center rounded-md border transition-colors",
              viewMode === "list" ? "border-accent bg-accent/15 text-accent" : "border-line text-muted hover:text-ink",
            )}
          >
            <LayoutList className="size-4" />
          </button>
          <button
            type="button"
            onClick={() => setViewMode("grid")}
            title="Grid / Gallery view"
            className={cx(
              "flex size-8 items-center justify-center rounded-md border transition-colors",
              viewMode === "grid" ? "border-accent bg-accent/15 text-accent" : "border-line text-muted hover:text-ink",
            )}
          >
            <LayoutGrid className="size-4" />
          </button>
        </div>
      </div>

      {/* Trash panel */}
      {showTrash && (
        <div className="mb-4 rounded-card border border-amber-500/30 bg-amber-500/5 p-4 space-y-3">
          <div className="flex items-center justify-between">
            <div className="flex items-center gap-2 text-sm font-semibold text-ink">
              <Archive className="size-4 text-amber-400" />
              Recycle Bin ({count(trashObjects.length)} items)
            </div>
            <div className="flex gap-2">
              <Button onClick={() => setShowTrash(false)}>Close</Button>
              {trashObjects.length > 0 && (
                <Button variant="danger" icon={<Trash2 className="size-3.5" />} onClick={emptyTrash}>
                  Empty Trash
                </Button>
              )}
            </div>
          </div>
          {trashObjects.length === 0 ? (
            <div className="py-4 text-center text-sm text-muted">Trash is empty.</div>
          ) : (
            <div className="divide-y divide-line rounded-md border border-line max-h-60 overflow-y-auto text-xs">
              {trashObjects.map((o) => {
                const original = o.key.replace(/^\.trash\//, "");
                return (
                  <div key={o.key} className="flex items-center justify-between px-3 py-2 hover:bg-sunken/40">
                    <div className="flex items-center gap-2 min-w-0">
                      <Trash2 className="size-3.5 text-muted shrink-0" />
                      <span className="truncate font-mono text-ink" title={original}>{original}</span>
                      <span className="text-[10px] text-muted shrink-0">{bytes(o.size)}</span>
                    </div>
                    <button
                      type="button"
                      disabled={restoringTrash}
                      onClick={() => restoreFromTrash(o.key)}
                      className="flex items-center gap-1 text-[11px] font-medium text-accent hover:underline shrink-0 ml-2"
                    >
                      <Undo2 className="size-3" /> Restore
                    </button>
                  </div>
                );
              })}
            </div>
          )}
        </div>
      )}

      {/* Selection bar */}
      {selected.size > 0 && (
        <div className="mb-3 flex flex-wrap items-center justify-between gap-3 rounded-md border border-accent/30 bg-accent-soft px-4 py-2 text-sm">
          <span className="font-medium text-accent-ink">{count(selected.size)} selected</span>
          <div className="flex flex-wrap items-center gap-2">
            {selected.size === 2 && (
              <Button
                icon={<GitCompare className="size-4" />}
                onClick={() => setDiffFiles(Array.from(selected))}
              >
                Compare (Diff)
              </Button>
            )}
            {selected.size === 1 && !([...selected][0].endsWith("/")) && (
              <Button
                icon={<Share2 className="size-4" />}
                onClick={async () => {
                  const key = [...selected][0];
                  try {
                    const st = await api.stat(bucket, key);
                    setSharing(st);
                  } catch {
                    setSharing({ key, size: 0, etag: "", lastModified: new Date().toISOString() });
                  }
                }}
              >
                Share URL
              </Button>
            )}
            <Button
              icon={<Download className="size-4" />}
              busy={downloadingZip}
              onClick={downloadSelectedZip}
            >
              {downloadingZip ? "Zipping…" : "Download ZIP"}
            </Button>
            <Button
              icon={<FolderInput className="size-4" />}
              onClick={() => {
                setTransferTargetBucket(bucket);
                setTransferPrefix(prefix);
                setTransferOpen(true);
              }}
            >
              Copy / Move
            </Button>
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
        ) : viewMode === "grid" ? (
          <div className="p-4">
            <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-5 xl:grid-cols-6">
              {displayedPrefixes.map((p) => (
                <div
                  key={p}
                  onClick={() => goTo(p)}
                  className="group relative flex cursor-pointer flex-col items-center justify-center rounded-lg border border-line bg-surface p-4 text-center transition-colors hover:border-accent/40 hover:bg-sunken/60"
                >
                  <div
                    className="absolute top-2 left-2"
                    onClick={(e) => e.stopPropagation()}
                  >
                    <input
                      type="checkbox"
                      name="selected"
                      aria-label={`Select ${p}`}
                      checked={selected.has(p)}
                      onChange={() => toggle(p)}
                      className="size-4 accent-[var(--accent)]"
                    />
                  </div>
                  <Folder className="size-10 fill-amber-400 text-amber-500 mb-2" />
                  <span className="w-full truncate text-xs font-medium text-ink group-hover:text-accent-ink" title={p}>
                    {search ? p : baseName(p)}/
                  </span>
                </div>
              ))}
              {displayedObjects.map((o) => {
                const kind = kindOf(o.key, o.contentType);
                const Icon = kindIcons[kind];
                const isImg = kind === "image";
                const isSelected = selected.has(o.key);
                return (
                  <div
                    key={o.key}
                    onClick={() => setDetail(o.key)}
                    className={cx(
                      "group relative flex cursor-pointer flex-col overflow-hidden rounded-lg border transition-all hover:shadow-md",
                      isSelected
                        ? "border-accent bg-accent/5 ring-1 ring-accent"
                        : detail === o.key
                        ? "border-accent bg-accent-soft/40"
                        : "border-line bg-surface hover:border-line-strong hover:bg-sunken/40"
                    )}
                  >
                    <div
                      className="absolute top-2 left-2 z-10"
                      onClick={(e) => e.stopPropagation()}
                    >
                      <input
                        type="checkbox"
                        name="selected"
                        aria-label={`Select ${o.key}`}
                        checked={isSelected}
                        onChange={() => toggle(o.key)}
                        className="size-4 accent-[var(--accent)]"
                      />
                    </div>
                    <div className="absolute top-2 right-2 z-10 opacity-0 group-hover:opacity-100 transition-opacity">
                      <button
                        type="button"
                        onClick={(e) => {
                          e.stopPropagation();
                          setSharing(o);
                        }}
                        className="flex size-7 items-center justify-center rounded-full bg-surface/90 text-muted shadow hover:text-accent backdrop-blur transition-colors"
                        title="Share Presigned URL"
                      >
                        <Share2 className="size-3.5" />
                      </button>
                    </div>
                    <div className="flex h-28 w-full items-center justify-center overflow-hidden bg-sunken/80">
                      {isImg ? (
                        <img
                          src={api.downloadURL(bucket, o.key)}
                          alt={baseName(o.key)}
                          loading="lazy"
                          className="h-full w-full object-cover transition-transform duration-200 group-hover:scale-105"
                        />
                      ) : (
                        <Icon className="size-10 text-muted transition-colors group-hover:text-accent" />
                      )}
                    </div>
                    <div className="p-2.5">
                      <div className="truncate text-xs font-medium text-ink" title={o.key}>
                        {search ? o.key : baseName(o.key)}
                      </div>
                      <div className="mt-1 flex items-center justify-between text-[11px] text-muted">
                        <span>{bytes(o.size)}</span>
                        <span>{relative(o.lastModified)}</span>
                      </div>
                    </div>
                  </div>
                );
              })}
            </div>
          </div>
        ) : (
          <table className="w-full text-sm">
            <thead className="border-b border-line bg-sunken/60 text-left">
              <tr className="label-strip">
                <th className="w-10 py-2 pl-4">
                  <input
                    type="checkbox"
                    name="select-all"
                    aria-label="Select all"
                    checked={allSelected}
                    onChange={() => setSelected(allSelected ? new Set() : new Set(allKeys))}
                    className="size-4 accent-[var(--accent)]"
                  />
                </th>
                <th className="py-2 pr-3 font-semibold text-ink">▲ Name</th>
                <th className="hidden w-44 py-2 pr-4 font-normal md:table-cell">Last Modified</th>
                <th className="hidden w-36 py-2 pr-4 text-right font-normal sm:table-cell">Size</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-line">
              {displayedPrefixes.map((p) => (
                <tr key={p} className="group hover:bg-sunken/60">
                  <td className="py-2.5 pl-4">
                    <input type="checkbox" name="selected" aria-label={`Select ${p}`} checked={selected.has(p)} onChange={() => toggle(p)} className="size-4 accent-[var(--accent)]" />
                  </td>
                  <td className="py-2.5 pr-3">
                    <button onClick={() => goTo(p)} className="flex max-w-full items-center gap-2.5 text-left">
                      <Folder className="size-4 shrink-0 fill-amber-400 text-amber-500" />
                      <span className="truncate font-medium text-ink group-hover:text-accent-ink">{search ? p : baseName(p)}/</span>
                    </button>
                  </td>
                  <td className="hidden py-2.5 pr-4 text-faint md:table-cell">-</td>
                  <td className="hidden py-2.5 pr-4 text-right text-faint sm:table-cell">-</td>
                </tr>
              ))}
              {displayedObjects.map((o) => {
                const Icon = kindIcons[kindOf(o.key, o.contentType)];
                return (
                  <tr key={o.key} className={cx("group cursor-pointer hover:bg-sunken/60", detail === o.key && "bg-accent-soft/60")} onClick={() => setDetail(o.key)}>
                    <td className="py-2.5 pl-4" onClick={(e) => e.stopPropagation()}>
                      <input type="checkbox" name="selected" aria-label={`Select ${o.key}`} checked={selected.has(o.key)} onChange={() => toggle(o.key)} className="size-4 accent-[var(--accent)]" />
                    </td>
                    <td className="max-w-0 py-2.5 pr-3">
                      <div className="flex items-center gap-2.5">
                        <Icon className="size-4 shrink-0 text-faint" />
                        <span className="truncate font-medium text-ink" title={o.key}>
                          {search ? o.key : baseName(o.key)}
                        </span>
                      </div>
                      <div className="num mt-0.5 pl-6.5 text-[11px] text-muted sm:hidden">
                        {bytes(o.size)} · {relative(o.lastModified)}
                      </div>
                    </td>
                    <td className="hidden py-2.5 pr-4 text-muted md:table-cell" title={dateTime(o.lastModified)}>
                      {dateTime(o.lastModified)}
                    </td>
                    <td className="num hidden py-2.5 pr-4 text-right text-muted sm:table-cell">
                      <div className="flex items-center justify-end gap-1.5">
                        <span>{bytes(o.size)}</span>
                        <IconButton
                          label="Share"
                          onClick={(e) => {
                            e.stopPropagation();
                            setSharing(o);
                          }}
                          className="opacity-0 group-hover:opacity-100 hover:text-accent transition-opacity size-7"
                        >
                          <Share2 className="size-3.5" />
                        </IconButton>
                      </div>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        )}
        {loading && displayedObjects.length + displayedPrefixes.length === 0 && !empty && (
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
          onDiff={setDiffFiles}
          onDeleted={() => {
            setDetail(null);
            load();
          }}
        />
      )}
      <ShareModal bucket={bucket} object={sharing} onClose={() => setSharing(null)} />
      <FileDiffModal bucket={bucket} files={diffFiles} onClose={() => setDiffFiles(null)} />
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
            <Button variant="danger" icon={<Trash2 className="size-4" />} busy={deletingSelected} onClick={softDeleteSelected}>
              {deletingSelected ? "Moving to trash…" : `Move to Trash (${count(selected.size)})`}
            </Button>
          </>
        }
      >
        <p className="text-sm">
          Move {count(selected.size)} selected item{selected.size === 1 ? "" : "s"} to trash?
          {[...selected].some((k) => k.endsWith("/")) && " Folders are deleted with everything inside them."}
        </p>
        <p className="text-xs text-muted mt-2">Files will be moved to <span className="font-mono">.trash/</span> and can be restored later.</p>
      </Modal>

      <Modal
        open={transferOpen}
        onClose={() => !transferBusy && setTransferOpen(false)}
        title={`${transferMode === "copy" ? "Copy" : "Move"} ${count(selected.size)} items`}
        footer={
          <>
            <Button disabled={transferBusy} onClick={() => setTransferOpen(false)}>
              Cancel
            </Button>
            <Button
              variant="primary"
              busy={transferBusy}
              icon={<FolderInput className="size-4" />}
              onClick={executeTransfer}
            >
              {transferBusy ? "Transferring…" : transferMode === "copy" ? "Copy Objects" : "Move Objects"}
            </Button>
          </>
        }
      >
        <div className="space-y-4">
          <div>
            <span className="label-strip mb-1.5 block">Transfer Action</span>
            <div className="grid grid-cols-2 gap-2 text-xs">
              <button
                type="button"
                onClick={() => setTransferMode("copy")}
                className={cx(
                  "flex items-center justify-center gap-1.5 py-2 rounded-lg border font-medium transition-colors",
                  transferMode === "copy"
                    ? "border-accent bg-accent/15 text-accent font-semibold"
                    : "border-line bg-card text-muted hover:text-ink"
                )}
              >
                <Copy className="size-3.5" /> Copy (Keep originals)
              </button>
              <button
                type="button"
                onClick={() => setTransferMode("move")}
                className={cx(
                  "flex items-center justify-center gap-1.5 py-2 rounded-lg border font-medium transition-colors",
                  transferMode === "move"
                    ? "border-accent bg-accent/15 text-accent font-semibold"
                    : "border-line bg-card text-muted hover:text-ink"
                )}
              >
                <FolderInput className="size-3.5" /> Move (Delete originals)
              </button>
            </div>
          </div>

          <div>
            <label htmlFor="explorer-destination-bucket" className="label-strip mb-1.5 block">Destination Bucket</label>
            <Select
              id="explorer-destination-bucket"
              aria-label="Destination Bucket"
              value={transferTargetBucket}
              onChange={setTransferTargetBucket}
              options={allBuckets.map((b) => ({ value: b.name, label: b.name }))}
            />
          </div>

          <div>
            <label htmlFor="explorer-destination-folder-prefix" className="label-strip mb-1.5 block">Destination Folder Prefix (Optional)</label>
            <Input
              id="explorer-destination-folder-prefix"
              value={transferPrefix}
              onChange={(e) => setTransferPrefix(e.target.value)}
              placeholder="e.g. archive/2026/ or leave empty for root"
              className="font-mono text-xs"
            />
          </div>

          <div className="rounded-md border border-line bg-sunken/40 p-3 text-xs text-muted">
            <span className="font-semibold text-ink">Target path: </span>
            <span className="font-mono">
              s3://{transferTargetBucket}/{transferPrefix ? (transferPrefix.endsWith("/") ? transferPrefix : transferPrefix + "/") : ""}[filename]
            </span>
          </div>
        </div>
      </Modal>
      <BucketAnalyticsModal open={analyticsOpen} onClose={() => setAnalyticsOpen(false)} bucket={bucket} objects={objects} meta={meta} />
    </div>
  );
}
