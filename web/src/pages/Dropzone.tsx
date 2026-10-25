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
} from "lucide-react";
import { api, dropzoneUpload, type Bucket, type Dropzone, type PublicDropzone } from "../api";
import { PageHeader, Card, Button, Input, Modal, Spinner, cx, errorText, useToast } from "../components/ui";
import { Select } from "../components/Select";
import { bytes, dateTime } from "../lib/format";

type QueuedFile = { file: File; progress: number; status: "pending" | "uploading" | "done" | "error"; detail?: string };

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

  function update(i: number, patch: Partial<QueuedFile>) {
    setFiles((prev) => prev.map((f, j) => (j === i ? { ...f, ...patch } : f)));
  }

  function addFiles(list: FileList | null) {
    if (!list || !info) return;
    const added = Array.from(list).map<QueuedFile>((file) =>
      file.size > info.maxBytes
        ? { file, progress: 0, status: "error", detail: `Larger than ${bytes(info.maxBytes)}` }
        : { file, progress: 0, status: "pending" },
    );
    setFiles((prev) => [...prev, ...added]);
  }

  async function uploadAll() {
    setIsUploading(true);
    for (let i = 0; i < files.length; i++) {
      const { file, status } = files[i];
      if (status !== "pending") continue;
      update(i, { status: "uploading", progress: 0 });
      try {
        const stored = await dropzoneUpload(
          token,
          file,
          (sent) => update(i, { progress: Math.round((sent / Math.max(file.size, 1)) * 100) }),
          new AbortController().signal,
        );
        update(i, { status: "done", progress: 100, detail: stored !== file.name ? `Saved as ${stored}` : undefined });
      } catch (e) {
        update(i, { status: "error", detail: errorText(e) });
      }
    }
    setIsUploading(false);
  }

  const pending = files.filter((f) => f.status === "pending").length;

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
                  <div className="flex items-center justify-between text-xs font-medium text-muted">
                    <span>Files ({files.length})</span>
                    {!isUploading && (
                      <button onClick={() => setFiles([])} className="hover:text-danger transition-colors">
                        Clear all
                      </button>
                    )}
                  </div>

                  <div className="divide-y divide-line rounded-lg border border-line max-h-52 overflow-y-auto">
                    {files.map((item, idx) => (
                      <div key={idx} className="flex items-center justify-between p-3 text-xs">
                        <div className="flex items-center gap-2 min-w-0 pr-4">
                          <FileText className="size-4 shrink-0 text-muted" />
                          <span className="font-mono truncate">{item.file.name}</span>
                          <span className="text-[10px] text-muted">({bytes(item.file.size)})</span>
                        </div>
                        <div className="shrink-0 text-right">
                          {item.status === "done" ? (
                            <span className="flex items-center gap-1 text-ok font-medium">
                              <Check className="size-3.5" /> Uploaded
                            </span>
                          ) : item.status === "uploading" ? (
                            <span className="font-mono text-accent">{item.progress}%</span>
                          ) : item.status === "error" ? (
                            <span className="text-danger font-medium">Not uploaded</span>
                          ) : (
                            <span className="text-muted">Pending</span>
                          )}
                          {item.detail && <div className="text-[10px] text-muted">{item.detail}</div>}
                        </div>
                      </div>
                    ))}
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
  const toast = useToast();

  const load = () =>
    api
      .dropzones()
      .then(setLinks)
      .catch((e) => {
        setLinks([]);
        toast("error", errorText(e));
      });

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

              <div className="grid grid-cols-2 gap-2 text-xs border-t border-line pt-3 font-mono">
                <div>
                  <span className="text-muted block text-[10px]">Expires:</span>
                  <span>{dateTime(link.expiresAt)}</span>
                </div>
                <div>
                  <span className="text-muted block text-[10px]">Limit:</span>
                  <span>{bytes(link.maxBytes)} per file</span>
                </div>
              </div>

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
