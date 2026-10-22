import { useState, useEffect, useRef } from "react";
import { useSearchParams } from "react-router";
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
import { api, uploadFile, type Bucket } from "../api";
import { PageHeader, Card, Button, Input, Modal, cx, useToast } from "../components/ui";
import { Select } from "../components/Select";
import { bytes, dateTime } from "../lib/format";

interface DropzoneLink {
  id: string;
  bucket: string;
  prefix: string;
  expiresAt: string;
  maxSizeBytes: number;
  note: string;
}

export function DropzonePage() {
  const [searchParams] = useSearchParams();
  const isPublicView = searchParams.get("public") === "1" || searchParams.get("token") !== null;
  const paramBucket = searchParams.get("bucket") || "";
  const paramPrefix = searchParams.get("prefix") || "";

  // Admin state
  const [buckets, setBuckets] = useState<Bucket[]>([]);
  const [links, setLinks] = useState<DropzoneLink[]>(() => {
    try {
      const saved = localStorage.getItem("bin_dropzone_links");
      return saved ? JSON.parse(saved) : [];
    } catch {
      return [];
    }
  });
  const [createModal, setCreateModal] = useState(false);
  const [selectedBucket, setSelectedBucket] = useState("");
  const [selectedPrefix, setSelectedPrefix] = useState("");
  const [expiryHours, setExpiryHours] = useState("24");
  const [maxSizeMB, setMaxSizeMB] = useState("100");
  const [note, setNote] = useState("");
  const [copiedId, setCopiedId] = useState<string | null>(null);

  // Upload dropzone state (for public or admin dropzone)
  const [dragOver, setDragOver] = useState(false);
  const [filesToUpload, setFilesToUpload] = useState<{ file: File; progress: number; status: "pending" | "uploading" | "done" | "error" }[]>([]);
  const [isUploading, setIsUploading] = useState(false);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const toast = useToast();

  useEffect(() => {
    try {
      localStorage.setItem("bin_dropzone_links", JSON.stringify(links));
    } catch {}
  }, [links]);

  useEffect(() => {
    api.buckets().then((list) => {
      setBuckets(list);
      if (list.length > 0 && !selectedBucket) setSelectedBucket(list[0].name);
    }).catch(() => {});
  }, [selectedBucket]);

  function createLink() {
    if (!selectedBucket) {
      toast("error", "Please select a bucket.");
      return;
    }

    const expDate = new Date(Date.now() + Number(expiryHours) * 3600 * 1000).toISOString();
    const newLink: DropzoneLink = {
      id: "dz_" + Math.random().toString(36).substring(2, 9),
      bucket: selectedBucket,
      prefix: selectedPrefix ? (selectedPrefix.endsWith("/") ? selectedPrefix : selectedPrefix + "/") : "",
      expiresAt: expDate,
      maxSizeBytes: Number(maxSizeMB) * 1024 * 1024,
      note: note.trim() || "Upload your files here",
    };

    setLinks((prev) => [newLink, ...prev]);
    setCreateModal(false);
    toast("ok", "Dropzone upload link created!");
  }

  function getShareUrl(link: DropzoneLink) {
    const origin = window.location.origin;
    return `${origin}/dropzone?public=1&bucket=${encodeURIComponent(link.bucket)}&prefix=${encodeURIComponent(link.prefix)}&token=${link.id}`;
  }

  function copyLink(link: DropzoneLink) {
    const url = getShareUrl(link);
    navigator.clipboard.writeText(url).then(() => {
      setCopiedId(link.id);
      toast("ok", "Upload link copied to clipboard!");
      setTimeout(() => setCopiedId(null), 2000);
    });
  }

  function handleFileSelect(files: FileList | null) {
    if (!files) return;
    const list = Array.from(files).map((f) => ({
      file: f,
      progress: 0,
      status: "pending" as const,
    }));
    setFilesToUpload((prev) => [...prev, ...list]);
  }

  async function startUploadQueue() {
    const targetBucket = paramBucket || selectedBucket;
    const targetPrefix = paramPrefix || selectedPrefix;

    if (!targetBucket) {
      toast("error", "No destination bucket available.");
      return;
    }

    setIsUploading(true);
    for (let i = 0; i < filesToUpload.length; i++) {
      if (filesToUpload[i].status === "done") continue;

      setFilesToUpload((prev) => {
        const copy = [...prev];
        copy[i].status = "uploading";
        return copy;
      });

      try {
        const f = filesToUpload[i].file;
        const key = targetPrefix + f.name;
        const abortCtrl = new AbortController();

        await uploadFile(
          targetBucket,
          key,
          f,
          (sent) => {
            const pct = Math.round((sent / f.size) * 100);
            setFilesToUpload((prev) => {
              const copy = [...prev];
              copy[i].progress = pct;
              return copy;
            });
          },
          abortCtrl.signal
        );

        setFilesToUpload((prev) => {
          const copy = [...prev];
          copy[i].status = "done";
          copy[i].progress = 100;
          return copy;
        });
      } catch (err: any) {
        setFilesToUpload((prev) => {
          const copy = [...prev];
          copy[i].status = "error";
          return copy;
        });
        toast("error", `Failed to upload ${filesToUpload[i].file.name}`);
      }
    }
    setIsUploading(false);
    toast("ok", "Upload complete!");
  }

  // --- Public Dropzone View ---
  if (isPublicView) {
    const targetBucket = paramBucket || "storage";
    const targetPrefix = paramPrefix || "";

    return (
      <div className="min-h-[80vh] flex flex-col items-center justify-center p-4">
        <div className="w-full max-w-2xl space-y-6">
          <div className="text-center space-y-2">
            <div className="mx-auto flex size-12 items-center justify-center rounded-2xl bg-accent text-white shadow-lg shadow-accent/25">
              <UploadCloud className="size-6" />
            </div>
            <h1 className="text-2xl font-bold tracking-tight text-ink">Upload Dropzone</h1>
            <p className="text-xs text-muted">
              Securely upload files to <span className="font-mono text-ink font-semibold">s3://{targetBucket}/{targetPrefix}</span>
            </p>
          </div>

          <Card className="p-6 space-y-6">
            {/* Drag & Drop Target */}
            <div
              onDragOver={(e) => { e.preventDefault(); setDragOver(true); }}
              onDragLeave={() => setDragOver(false)}
              onDrop={(e) => {
                e.preventDefault();
                setDragOver(false);
                handleFileSelect(e.dataTransfer.files);
              }}
              onClick={() => fileInputRef.current?.click()}
              className={cx(
                "border-2 border-dashed rounded-xl p-10 flex flex-col items-center justify-center cursor-pointer transition-colors text-center",
                dragOver
                  ? "border-accent bg-accent/5 ring-4 ring-accent/15"
                  : "border-line hover:border-accent/60 bg-sunken/30"
              )}
            >
              <input
                ref={fileInputRef}
                type="file"
                multiple
                hidden
                onChange={(e) => handleFileSelect(e.target.files)}
              />
              <UploadCloud className="size-10 text-muted mb-3" />
              <div className="font-semibold text-sm text-ink">Drag and drop files here</div>
              <div className="text-xs text-muted mt-1">or click to browse from your device</div>
            </div>

            {/* Selected Files List */}
            {filesToUpload.length > 0 && (
              <div className="space-y-3">
                <div className="flex items-center justify-between text-xs font-medium text-muted">
                  <span>Selected Files ({filesToUpload.length})</span>
                  <button
                    onClick={() => setFilesToUpload([])}
                    className="hover:text-danger transition-colors"
                  >
                    Clear All
                  </button>
                </div>

                <div className="divide-y divide-line rounded-lg border border-line max-h-52 overflow-y-auto">
                  {filesToUpload.map((item, idx) => (
                    <div key={idx} className="flex items-center justify-between p-3 text-xs">
                      <div className="flex items-center gap-2 min-w-0 pr-4">
                        <FileText className="size-4 shrink-0 text-muted" />
                        <span className="font-mono truncate">{item.file.name}</span>
                        <span className="text-[10px] text-muted">({bytes(item.file.size)})</span>
                      </div>
                      <div className="flex items-center gap-3 shrink-0">
                        {item.status === "done" ? (
                          <span className="flex items-center gap-1 text-ok font-medium">
                            <Check className="size-3.5" /> Uploaded
                          </span>
                        ) : item.status === "uploading" ? (
                          <span className="font-mono text-accent">{item.progress}%</span>
                        ) : item.status === "error" ? (
                          <span className="text-danger font-medium">Error</span>
                        ) : (
                          <span className="text-muted">Pending</span>
                        )}
                      </div>
                    </div>
                  ))}
                </div>

                <Button
                  variant="primary"
                  busy={isUploading}
                  onClick={startUploadQueue}
                  className="w-full justify-center"
                >
                  {isUploading ? "Uploading Files..." : `Upload ${filesToUpload.length} Files`}
                </Button>
              </div>
            )}
          </Card>
        </div>
      </div>
    );
  }

  // --- Admin Manager View ---
  return (
    <div className="space-y-6 max-w-5xl pb-16">
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4">
        <PageHeader
          title="Upload Dropzone Links"
          subtitle="Generate secure, temporary upload links allowing external partners to submit files directly into your buckets without account access."
        />
        <Button
          variant="primary"
          icon={<Plus className="size-4" />}
          onClick={() => setCreateModal(true)}
          className="shrink-0"
        >
          Create Dropzone Link
        </Button>
      </div>

      {/* Active Dropzone Links */}
      {links.length === 0 ? (
        <Card className="py-16 text-center text-muted text-sm space-y-3">
          <UploadCloud className="size-10 mx-auto text-faint" />
          <div className="font-semibold text-ink">No upload dropzones created yet</div>
          <div className="text-xs text-muted max-w-sm mx-auto">
            Create an upload link to let clients, contractors, or web forms upload files securely to your S3 storage.
          </div>
          <Button variant="primary" icon={<Plus className="size-4" />} onClick={() => setCreateModal(true)}>
            Create First Dropzone
          </Button>
        </Card>
      ) : (
        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
          {links.map((link) => {
            const isExpired = new Date(link.expiresAt).getTime() < Date.now();
            return (
              <Card key={link.id} className="p-5 space-y-4">
                <div className="flex items-start justify-between">
                  <div>
                    <div className="flex items-center gap-2">
                      <span className="font-mono font-semibold text-ink text-sm">
                        s3://{link.bucket}/{link.prefix}
                      </span>
                      <span className={cx(
                        "rounded-full px-2 py-0.5 text-[10px] font-semibold",
                        isExpired ? "bg-danger/10 text-danger" : "bg-ok/10 text-ok"
                      )}>
                        {isExpired ? "Expired" : "Active"}
                      </span>
                    </div>
                    <p className="text-xs text-muted mt-1">{link.note}</p>
                  </div>
                  <button
                    onClick={() => setLinks((prev) => prev.filter((l) => l.id !== link.id))}
                    className="text-muted hover:text-danger transition-colors"
                    title="Delete link"
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
                    <span>{bytes(link.maxSizeBytes)} per file</span>
                  </div>
                </div>

                <div className="flex items-center gap-2 pt-2">
                  <Button
                    variant="secondary"
                    icon={copiedId === link.id ? <Check className="size-3.5 text-ok" /> : <Copy className="size-3.5" />}
                    onClick={() => copyLink(link)}
                    className="flex-1 justify-center text-xs"
                  >
                    {copiedId === link.id ? "Copied!" : "Copy Public Link"}
                  </Button>
                  <a
                    href={getShareUrl(link)}
                    target="_blank"
                    rel="noreferrer"
                    className="contents"
                  >
                    <Button icon={<ExternalLink className="size-3.5" />} title="Open public view" />
                  </a>
                </div>
              </Card>
            );
          })}
        </div>
      )}

      {/* Create Modal */}
      <Modal
        open={createModal}
        onClose={() => setCreateModal(false)}
        title="Create Dropzone Upload Link"
        footer={
          <>
            <Button onClick={() => setCreateModal(false)}>Cancel</Button>
            <Button variant="primary" icon={<Share2 className="size-4" />} onClick={createLink}>
              Generate Link
            </Button>
          </>
        }
      >
        <div className="space-y-4">
          <div>
            <label className="label-strip mb-1.5 block">Target Bucket</label>
            <Select
              aria-label="Target Bucket"
              value={selectedBucket}
              onChange={setSelectedBucket}
              options={buckets.map((b) => ({ value: b.name, label: b.name }))}
            />
          </div>

          <div>
            <label className="label-strip mb-1.5 block">Folder Prefix (Optional)</label>
            <Input
              value={selectedPrefix}
              onChange={(e) => setSelectedPrefix(e.target.value)}
              placeholder="e.g. client-submissions/2026/"
              className="font-mono text-xs"
            />
          </div>

          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="label-strip mb-1.5 block">Link Expiration</label>
              <Select
                aria-label="Link Expiration"
                value={expiryHours}
                onChange={setExpiryHours}
                options={[
                  { value: "1", label: "1 Hour" },
                  { value: "24", label: "24 Hours (1 Day)" },
                  { value: "72", label: "3 Days" },
                  { value: "168", label: "7 Days" },
                ]}
              />
            </div>

            <div>
              <label className="label-strip mb-1.5 block">Max File Size</label>
              <Select
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
            <label className="label-strip mb-1.5 block">Note / Instructions for Uploader</label>
            <Input
              value={note}
              onChange={(e) => setNote(e.target.value)}
              placeholder="e.g. Please upload your documents for verification"
            />
          </div>
        </div>
      </Modal>
    </div>
  );
}
