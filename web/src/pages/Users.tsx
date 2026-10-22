import { useEffect, useState } from "react";
import {
  Eye,
  EyeOff,
  Plus,
  RefreshCw,
  Search,
  Shield,
  Trash2,
  TriangleAlert,
  User,
  UserCheck,
  UserPlus,
  Users as UsersIcon,
} from "lucide-react";
import { api, type AccessKey } from "../api";
import { Button, IconButton, Input, Modal, Spinner, cx, errorText, useToast } from "../components/ui";

const AVAILABLE_POLICIES = [
  { name: "consoleAdmin", desc: "Full administrative access to console and S3 APIs" },
  { name: "diagnostics", desc: "Access to diagnostics, metrics and server info" },
  { name: "readonly", desc: "Read-only access to all buckets and objects" },
  { name: "readwrite", desc: "Read and write access to all buckets and objects" },
  { name: "writeonly", desc: "Write-only permissions (uploads and deletes)" },
];

function generateSecret(): string {
  const chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789!@#$%^&*()_+";
  let s = "";
  for (let i = 0; i < 32; i++) s += chars.charAt(Math.floor(Math.random() * chars.length));
  return s;
}

export function UsersPage() {
  const [users, setUsers] = useState<AccessKey[] | null>(null);
  const [loading, setLoading] = useState(true);
  const [search, setSearch] = useState("");
  const [view, setView] = useState<"list" | "create">("list");
  const [selectedUsers, setSelectedUsers] = useState<Set<string>>(new Set());

  // Create User Form State
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [showPassword, setShowPassword] = useState(false);
  const [policySearch, setPolicySearch] = useState("");
  const [assignedPolicies, setAssignedPolicies] = useState<string[]>(["readwrite"]);
  const [saving, setSaving] = useState(false);
  const [createdUser, setCreatedUser] = useState<AccessKey | null>(null);
  const toast = useToast();

  const load = () => {
    setLoading(true);
    api
      .accessKeys()
      .then(setUsers)
      .catch((e) => toast("error", errorText(e)))
      .finally(() => setLoading(false));
  };

  useEffect(() => {
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const filteredUsers = (users ?? []).filter(
    (u) =>
      u.name.toLowerCase().includes(search.toLowerCase()) ||
      u.accessKey.toLowerCase().includes(search.toLowerCase()),
  );

  const filteredPolicies = AVAILABLE_POLICIES.filter((p) =>
    p.name.toLowerCase().includes(policySearch.toLowerCase()),
  );

  function togglePolicy(name: string) {
    setAssignedPolicies((prev) =>
      prev.includes(name) ? prev.filter((p) => p !== name) : [...prev, name],
    );
  }

  function handleClear() {
    setUsername("");
    setPassword("");
    setAssignedPolicies(["readwrite"]);
    setPolicySearch("");
  }

  async function handleSaveUser() {
    const trimmedUser = username.trim();
    if (!trimmedUser) {
      toast("error", "User name is required.");
      return;
    }

    setSaving(true);
    const isReadOnly = assignedPolicies.includes("readonly") && !assignedPolicies.includes("readwrite") && !assignedPolicies.includes("consoleAdmin");

    try {
      const res = await api.createAccessKey({
        name: trimmedUser,
        readOnly: isReadOnly,
        buckets: [],
      });
      setCreatedUser(res);
      toast("ok", `User ${trimmedUser} created successfully.`);
      load();
      setView("list");
      handleClear();
    } catch (e) {
      toast("error", errorText(e));
    } finally {
      setSaving(false);
    }
  }

  async function handleDeleteSelected() {
    if (selectedUsers.size === 0) return;
    try {
      for (const ak of selectedUsers) {
        await api.deleteAccessKey(ak);
      }
      toast("ok", `${selectedUsers.size} user(s) deleted.`);
      setSelectedUsers(new Set());
      load();
    } catch (e) {
      toast("error", errorText(e));
    }
  }

  async function toggleUserStatus(u: AccessKey) {
    try {
      await api.updateAccessKey(u.accessKey, { enabled: !u.enabled });
      load();
      toast("ok", `User ${u.name || u.accessKey} ${!u.enabled ? "enabled" : "disabled"}.`);
    } catch (e) {
      toast("error", errorText(e));
    }
  }

  if (view === "create") {
    return (
      <div className="space-y-6">
        <button
          type="button"
          onClick={() => setView("list")}
          className="label-strip flex items-center gap-1.5 hover:text-ink text-muted transition-colors"
        >
          ← Users
        </button>

        <div className="grid grid-cols-1 gap-8 lg:grid-cols-3">
          {/* Main Form (2 cols) */}
          <div className="space-y-6 lg:col-span-2">
            <div className="flex items-center gap-2.5">
              <div className="flex size-9 items-center justify-center rounded-lg bg-accent/10 text-accent">
                <UserPlus className="size-5" />
              </div>
              <h1 className="text-xl font-bold tracking-tight text-ink">Create User</h1>
            </div>

            <div className="space-y-5 rounded-card border border-line bg-surface p-5 shadow-sm">
              {/* User Name */}
              <div>
                <label className="mb-1.5 block text-sm font-medium text-ink">User Name</label>
                <Input
                  autoFocus
                  value={username}
                  onChange={(e) => setUsername(e.target.value)}
                  placeholder="e.g. app-backup-service"
                />
              </div>

              {/* Password */}
              <div>
                <div className="mb-1.5 flex items-center justify-between">
                  <label className="text-sm font-medium text-ink">Password</label>
                  <button
                    type="button"
                    onClick={() => setPassword(generateSecret())}
                    className="text-xs text-accent hover:underline"
                  >
                    Generate random
                  </button>
                </div>
                <div className="relative">
                  <Input
                    type={showPassword ? "text" : "password"}
                    value={password}
                    onChange={(e) => setPassword(e.target.value)}
                    placeholder="Enter password or leave blank for auto-generation"
                    className="pr-10"
                  />
                  <button
                    type="button"
                    onClick={() => setShowPassword((p) => !p)}
                    className="absolute right-2.5 top-1/2 -translate-y-1/2 text-muted hover:text-ink"
                  >
                    {showPassword ? <EyeOff className="size-4" /> : <Eye className="size-4" />}
                  </button>
                </div>
              </div>

              {/* Assign Policies */}
              <div>
                <label className="mb-1.5 block text-sm font-medium text-ink">Assign Policies</label>
                <div className="rounded-md border border-line bg-surface overflow-hidden">
                  <div className="border-b border-line p-2">
                    <div className="relative">
                      <Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-faint" />
                      <input
                        type="text"
                        value={policySearch}
                        onChange={(e) => setPolicySearch(e.target.value)}
                        placeholder="Start typing to search for a Policy"
                        className="w-full bg-transparent pl-8 pr-3 text-xs text-ink placeholder:text-faint focus:outline-none"
                      />
                    </div>
                  </div>
                  <div className="max-h-52 overflow-y-auto divide-y divide-line">
                    <div className="grid grid-cols-[3rem_1fr] bg-sunken/60 px-3 py-1.5 text-[11px] font-semibold uppercase text-faint">
                      <span>Select</span>
                      <span>Policy</span>
                    </div>
                    {filteredPolicies.map((p) => (
                      <label
                        key={p.name}
                        className="grid grid-cols-[3rem_1fr] items-center px-3 py-2 text-sm hover:bg-sunken/50 cursor-pointer"
                      >
                        <input
                          type="checkbox"
                          checked={assignedPolicies.includes(p.name)}
                          onChange={() => togglePolicy(p.name)}
                          className="size-4 accent-[var(--accent)]"
                        />
                        <div className="min-w-0">
                          <div className="font-mono text-xs font-medium text-ink">{p.name}</div>
                          <div className="text-[11px] text-muted truncate">{p.desc}</div>
                        </div>
                      </label>
                    ))}
                  </div>
                </div>
              </div>

              <div className="text-xs text-faint">No Groups Available</div>

              {/* Actions */}
              <div className="flex justify-end gap-2.5 pt-2">
                <Button onClick={handleClear}>Clear</Button>
                <Button variant="primary" busy={saving} onClick={handleSaveUser}>
                  Save
                </Button>
              </div>
            </div>
          </div>

          {/* Learn More Sidebar (1 col) */}
          <div className="space-y-4 rounded-card border border-line bg-surface p-5 shadow-sm">
            <h3 className="flex items-center gap-2 text-sm font-semibold text-ink">
              <span className="flex size-5 items-center justify-center rounded-full bg-accent/10 text-xs font-bold text-accent">
                ?
              </span>
              Learn more about the Users feature
            </h3>
            <p className="text-xs leading-relaxed text-muted">
              A MinIO user consists of a unique access key (username) and corresponding secret key
              (password). Clients must authenticate their identity by specifying both a valid
              access key and secret key of an existing user.
            </p>
            <p className="text-xs leading-relaxed text-muted">
              Each user can have one or more assigned policies that explicitly list the actions and
              resources to which that user has access.
            </p>
            <div className="space-y-2 border-t border-line pt-3 text-xs text-ink">
              <div className="flex items-center gap-2">
                <UserCheck className="size-4 text-accent" />
                <span>Create Users</span>
              </div>
              <div className="flex items-center gap-2">
                <UsersIcon className="size-4 text-accent" />
                <span>Manage Groups</span>
              </div>
              <div className="flex items-center gap-2">
                <Shield className="size-4 text-accent" />
                <span>Assign Policies</span>
              </div>
            </div>
          </div>
        </div>
      </div>
    );
  }

  return (
    <div className="space-y-5">
      <div className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
        <h1 className="text-2xl font-bold tracking-tight text-ink">Users</h1>
        <div className="flex flex-wrap items-center gap-2">
          {selectedUsers.size > 0 && (
            <Button
              variant="danger"
              icon={<Trash2 className="size-4" />}
              onClick={handleDeleteSelected}
            >
              Delete Selected ({selectedUsers.size})
            </Button>
          )}
          <Button variant="primary" icon={<Plus className="size-4" />} onClick={() => setView("create")}>
            Create User
          </Button>
        </div>
      </div>

      {/* Search Bar */}
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="relative w-full max-w-sm">
          <Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-faint" />
          <Input
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="Search Users"
            className="pl-9 h-9"
          />
        </div>
        <IconButton label="Refresh" onClick={load}>
          <RefreshCw className={cx("size-4", loading && "animate-spin")} />
        </IconButton>
      </div>

      {/* Educational Banner (MinIO style) */}
      <div className="rounded-card border border-line bg-surface p-5 shadow-sm space-y-3">
        <div className="flex items-center gap-2.5">
          <div className="flex size-7 items-center justify-center rounded bg-accent/10 text-accent">
            <User className="size-4" />
          </div>
          <h2 className="text-sm font-semibold text-ink">Users</h2>
        </div>
        <p className="text-xs leading-relaxed text-muted">
          A user consists of a unique access key (username) and corresponding secret key (password).
          Clients must authenticate their identity by specifying both a valid access key and the
          corresponding secret key of an existing user. Groups provide a simplified method for managing
          shared permissions among users.
        </p>
        <p className="text-xs leading-relaxed text-muted">
          MinIO uses Policy-Based Access Control (PBAC) to define the authorized actions and resources
          to which an authenticated user has access. Each policy describes one or more actions and
          conditions that outline permissions.
        </p>
        <div className="pt-1">
          <button
            type="button"
            onClick={() => setView("create")}
            className="text-xs font-semibold text-accent hover:underline"
          >
            To get started, Create a User →
          </button>
        </div>
      </div>

      {/* Users Table */}
      <div className="overflow-hidden rounded-card border border-line bg-surface">
        {loading ? (
          <div className="flex justify-center py-12">
            <Spinner />
          </div>
        ) : filteredUsers.length === 0 ? (
          <div className="py-10 text-center text-sm text-faint">No users found.</div>
        ) : (
          <table className="w-full text-sm">
            <thead className="border-b border-line bg-sunken/60 text-left">
              <tr className="label-strip">
                <th className="w-10 py-2.5 pl-4">
                  <input
                    type="checkbox"
                    checked={selectedUsers.size === filteredUsers.length && filteredUsers.length > 0}
                    onChange={(e) =>
                      setSelectedUsers(
                        e.target.checked ? new Set(filteredUsers.map((u) => u.accessKey)) : new Set(),
                      )
                    }
                    className="size-4 accent-[var(--accent)]"
                  />
                </th>
                <th className="py-2.5 pr-3 font-semibold text-ink">User Name</th>
                <th className="py-2.5 pr-3 font-normal">Access Key</th>
                <th className="py-2.5 pr-3 font-normal">Status</th>
                <th className="py-2.5 pr-3 font-normal">Policy</th>
                <th className="py-2.5 pr-4 text-right font-normal">
                  <span className="sr-only">actions</span>
                </th>
              </tr>
            </thead>
            <tbody className="divide-y divide-line">
              {filteredUsers.map((u) => {
                const isSelected = selectedUsers.has(u.accessKey);
                return (
                  <tr key={u.accessKey} className="group hover:bg-sunken/60 transition-colors">
                    <td className="py-3 pl-4">
                      <input
                        type="checkbox"
                        checked={isSelected}
                        onChange={(e) => {
                          const next = new Set(selectedUsers);
                          if (e.target.checked) next.add(u.accessKey);
                          else next.delete(u.accessKey);
                          setSelectedUsers(next);
                        }}
                        className="size-4 accent-[var(--accent)]"
                      />
                    </td>
                    <td className="py-3 pr-3 font-medium text-ink">
                      <div className="flex items-center gap-2">
                        <User className="size-4 text-accent/70" />
                        <span>{u.name || "Untitled"}</span>
                      </div>
                    </td>
                    <td className="py-3 pr-3 font-mono text-xs text-muted">{u.accessKey}</td>
                    <td className="py-3 pr-3">
                      <button
                        type="button"
                        onClick={() => toggleUserStatus(u)}
                        className={cx(
                          "inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 text-xs font-medium cursor-pointer transition-colors",
                          u.enabled
                            ? "bg-ok-soft text-ok hover:bg-ok-soft/80"
                            : "bg-sunken text-muted hover:bg-line-strong",
                        )}
                      >
                        <span className={cx("size-1.5 rounded-full", u.enabled ? "bg-ok" : "bg-muted")} />
                        {u.enabled ? "Enabled" : "Disabled"}
                      </button>
                    </td>
                    <td className="py-3 pr-3">
                      <span className="rounded bg-sunken px-1.5 py-0.5 font-mono text-[11px] text-muted">
                        {u.readOnly ? "readonly" : "readwrite"}
                      </span>
                    </td>
                    <td className="py-3 pr-4 text-right">
                      <IconButton
                        label="Delete User"
                        onClick={async () => {
                          await api.deleteAccessKey(u.accessKey);
                          toast("ok", `User deleted.`);
                          load();
                        }}
                        className="hover:text-danger"
                      >
                        <Trash2 className="size-4" />
                      </IconButton>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        )}
      </div>

      {/* Secret Key Modal when user is created */}
      {createdUser && (
        <Modal
          open={!!createdUser}
          onClose={() => setCreatedUser(null)}
          title="User Created Successfully"
          wide={true}
          footer={
            <Button variant="primary" onClick={() => setCreatedUser(null)}>
              I saved the credentials
            </Button>
          }
        >
          <div className="space-y-4">
            <div className="flex gap-2.5 rounded-md border border-warn/30 bg-warn-soft px-3.5 py-2.5 text-sm text-warn">
              <TriangleAlert className="mt-0.5 size-4 shrink-0" />
              Copy the secret key now. It is stored encrypted and will not be displayed again.
            </div>
            <div>
              <div className="mb-1 text-xs font-medium text-muted">Access Key (Username)</div>
              <div className="flex items-center gap-2 rounded-md border border-line bg-sunken px-3 py-1.5 font-mono text-sm">
                <span className="flex-1 truncate">{createdUser.accessKey}</span>
              </div>
            </div>
            <div>
              <div className="mb-1 text-xs font-medium text-muted">Secret Key (Password)</div>
              <div className="flex items-center gap-2 rounded-md border border-line bg-sunken px-3 py-1.5 font-mono text-sm">
                <span className="flex-1 truncate">{createdUser.secretKey}</span>
              </div>
            </div>
          </div>
        </Modal>
      )}
    </div>
  );
}
