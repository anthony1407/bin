import { useState } from "react";
import { Check, Copy, FileText, Plus, Search, ShieldCheck, Trash2 } from "lucide-react";
import Editor from "../components/CodeEditor";
import { Button, Field, IconButton, Input, Modal, useToast } from "../components/ui";

interface PolicyItem {
  name: string;
  isBuiltin: boolean;
  content: string;
}

const DEFAULT_POLICIES: PolicyItem[] = [
  {
    name: "consoleAdmin",
    isBuiltin: true,
    content: JSON.stringify(
      {
        Version: "2012-10-17",
        Statement: [
          {
            Effect: "Allow",
            Action: ["admin:*", "s3:*"],
            Resource: ["arn:aws:s3:::*"],
          },
        ],
      },
      null,
      2,
    ),
  },
  {
    name: "diagnostics",
    isBuiltin: true,
    content: JSON.stringify(
      {
        Version: "2012-10-17",
        Statement: [
          {
            Effect: "Allow",
            Action: ["admin:ServerInfo", "admin:Metrics", "admin:Prometheus", "s3:GetBucketLocation"],
            Resource: ["arn:aws:s3:::*"],
          },
        ],
      },
      null,
      2,
    ),
  },
  {
    name: "readonly",
    isBuiltin: true,
    content: JSON.stringify(
      {
        Version: "2012-10-17",
        Statement: [
          {
            Effect: "Allow",
            Action: ["s3:GetBucketLocation", "s3:GetObject", "s3:ListBucket"],
            Resource: ["arn:aws:s3:::*"],
          },
        ],
      },
      null,
      2,
    ),
  },
  {
    name: "readwrite",
    isBuiltin: true,
    content: JSON.stringify(
      {
        Version: "2012-10-17",
        Statement: [
          {
            Effect: "Allow",
            Action: ["s3:*"],
            Resource: ["arn:aws:s3:::*"],
          },
        ],
      },
      null,
      2,
    ),
  },
  {
    name: "writeonly",
    isBuiltin: true,
    content: JSON.stringify(
      {
        Version: "2012-10-17",
        Statement: [
          {
            Effect: "Allow",
            Action: ["s3:PutObject", "s3:AbortMultipartUpload", "s3:DeleteObject"],
            Resource: ["arn:aws:s3:::*"],
          },
        ],
      },
      null,
      2,
    ),
  },
];

export function PoliciesPage() {
  const [policies, setPolicies] = useState<PolicyItem[]>(DEFAULT_POLICIES);
  const [search, setSearch] = useState("");
  const [selectedPolicy, setSelectedPolicy] = useState<PolicyItem | null>(null);
  const [createOpen, setCreateOpen] = useState(false);
  const [newName, setNewName] = useState("");
  const [newNameError, setNewNameError] = useState("");
  const [newContent, setNewContent] = useState(DEFAULT_POLICIES[2].content);
  const [copied, setCopied] = useState(false);
  const toast = useToast();

  const filtered = policies.filter((p) =>
    p.name.toLowerCase().includes(search.toLowerCase()),
  );

  function openCreate() {
    setNewName("");
    setNewNameError("");
    setNewContent(DEFAULT_POLICIES[2].content);
    setCreateOpen(true);
  }

  function handleSaveNew() {
    const trimmed = newName.trim();
    if (!trimmed) {
      setNewNameError("Policy name cannot be empty.");
      return;
    }
    if (policies.some((p) => p.name === trimmed)) {
      setNewNameError("A policy with this name already exists.");
      return;
    }
    setNewNameError("");
    try {
      JSON.parse(newContent);
    } catch {
      toast("error", "Invalid JSON content in policy.");
      return;
    }

    setPolicies((prev) => [...prev, { name: trimmed, isBuiltin: false, content: newContent }]);
    setCreateOpen(false);
    toast("ok", `Policy "${trimmed}" created.`);
  }

  function handleDelete(name: string) {
    setPolicies((prev) => prev.filter((p) => p.name !== name));
    if (selectedPolicy?.name === name) setSelectedPolicy(null);
    toast("ok", `Policy "${name}" deleted.`);
  }

  function copyText(text: string) {
    navigator.clipboard.writeText(text).then(() => {
      setCopied(true);
      toast("ok", "Policy copied to clipboard.");
      setTimeout(() => setCopied(false), 1500);
    });
  }

  return (
    <div className="space-y-5">
      <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
        <h1 className="text-2xl font-bold tracking-tight text-ink">IAM Policies</h1>
        <Button variant="primary" icon={<Plus className="size-4" />} onClick={openCreate}>
          Create Policy
        </Button>
      </div>

      {/* Search Bar */}
      <div className="relative max-w-sm">
        <Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-faint" />
        <Input
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          placeholder="Search Policies"
          className="pl-9 h-9"
        />
      </div>

      {/* Policies Table */}
      <div className="overflow-hidden rounded-card border border-line bg-surface">
        <table className="w-full text-sm">
          <thead className="border-b border-line bg-sunken/60 text-left">
            <tr className="label-strip">
              <th className="py-2.5 pl-4 pr-3 font-semibold text-ink">Name</th>
              <th className="py-2.5 pr-4 text-right font-normal">
                <span className="sr-only">actions</span>
              </th>
            </tr>
          </thead>
          <tbody className="divide-y divide-line">
            {filtered.length === 0 ? (
              <tr>
                <td colSpan={2} className="py-8 text-center text-sm text-faint">
                  No policies found.
                </td>
              </tr>
            ) : (
              filtered.map((p) => (
                <tr
                  key={p.name}
                  onClick={() => setSelectedPolicy(p)}
                  className="group cursor-pointer hover:bg-sunken/60 transition-colors"
                >
                  <td className="py-3 pl-4 pr-3 font-mono text-sm font-medium text-ink group-hover:text-accent">
                    <div className="flex items-center gap-2">
                      <ShieldCheck className="size-4 text-accent/80" />
                      <span>{p.name}</span>
                      {p.isBuiltin && (
                        <span className="rounded bg-sunken px-1.5 py-0.5 text-[10px] uppercase font-sans text-muted">
                          Built-in
                        </span>
                      )}
                    </div>
                  </td>
                  <td className="py-3 pr-4 text-right">
                    <div className="flex items-center justify-end gap-1" onClick={(e) => e.stopPropagation()}>
                      <IconButton
                        label="View Policy"
                        onClick={() => setSelectedPolicy(p)}
                      >
                        <FileText className="size-4" />
                      </IconButton>
                      {!p.isBuiltin && (
                        <IconButton
                          label="Delete"
                          onClick={() => handleDelete(p.name)}
                          className="hover:text-danger"
                        >
                          <Trash2 className="size-4" />
                        </IconButton>
                      )}
                    </div>
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>

      {/* View / Inspect Policy Modal with Monaco */}
      {selectedPolicy && (
        <Modal
          open={!!selectedPolicy}
          onClose={() => setSelectedPolicy(null)}
          title={`Policy: ${selectedPolicy.name}`}
          wide={true}
          footer={
            <div className="flex items-center justify-between w-full">
              <Button
                icon={copied ? <Check className="size-4 text-ok" /> : <Copy className="size-4" />}
                onClick={() => copyText(selectedPolicy.content)}
              >
                {copied ? "Copied" : "Copy Policy"}
              </Button>
              <Button onClick={() => setSelectedPolicy(null)}>Close</Button>
            </div>
          }
        >
          <div className="space-y-2">
            <div className="relative overflow-hidden rounded-md border border-line-strong bg-[#1e1e1e]">
              <Editor
                height="320px"
                defaultLanguage="json"
                language="json"
                theme="vs-dark"
                value={selectedPolicy.content}
                options={{
                  readOnly: true,
                  minimap: { enabled: false },
                  scrollBeyondLastLine: false,
                  fontSize: 12,
                  tabSize: 2,
                  lineNumbers: "on",
                  folding: true,
                  wordWrap: "on",
                  automaticLayout: true,
                }}
              />
            </div>
          </div>
        </Modal>
      )}

      {/* Create Policy Modal with Monaco */}
      <Modal
        open={createOpen}
        onClose={() => setCreateOpen(false)}
        title="Create Policy"
        wide={true}
        footer={
          <>
            <Button onClick={() => setCreateOpen(false)}>Cancel</Button>
            <Button variant="primary" onClick={handleSaveNew}>
              Save Policy
            </Button>
          </>
        }
      >
        <div className="space-y-4">
          <Field label="Policy Name" error={newNameError} hint="Unique name, alphanumeric and hyphens">
            <Input
              id="policies-policy-name"
              autoFocus
              value={newName}
              onChange={(e) => {
                setNewName(e.target.value);
                if (newNameError) setNewNameError("");
              }}
              placeholder="e.g. read-images-only"
            />
          </Field>
          <div>
            <span className="mb-1.5 block text-sm font-medium text-ink">Policy JSON</span>
            <div className="relative overflow-hidden rounded-md border border-line-strong bg-[#1e1e1e]">
              <Editor
                height="280px"
                defaultLanguage="json"
                language="json"
                theme="vs-dark"
                value={newContent}
                onChange={(val) => setNewContent(val ?? "")}
                options={{
                  minimap: { enabled: false },
                  scrollBeyondLastLine: false,
                  fontSize: 12,
                  tabSize: 2,
                  lineNumbers: "on",
                  folding: true,
                  wordWrap: "on",
                  automaticLayout: true,
                }}
              />
            </div>
          </div>
        </div>
      </Modal>
    </div>
  );
}
