import { useCallback, useEffect, useState } from "react";
import { Pencil, Plus, Trash2, Users as UsersIcon } from "lucide-react";
import { api, type AccessKey, type Bucket, type Group } from "../api";
import { date } from "../lib/format";
import { Badge, Button, EmptyState, Field, IconButton, Input, Modal, PageHeader, Spinner, cx, errorText, useToast } from "../components/ui";
import { MultiSelect } from "../components/Select";

interface Draft {
  name: string;
  description: string;
  buckets: string[];
  readOnly: boolean;
  members: string[];
}

const emptyDraft: Draft = { name: "", description: "", buckets: [], readOnly: false, members: [] };

const NAME_RULE = /^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$/;

export function GroupsPage() {
  const [groups, setGroups] = useState<Group[] | null>(null);
  const [keys, setKeys] = useState<AccessKey[]>([]);
  const [buckets, setBuckets] = useState<Bucket[]>([]);
  const [editing, setEditing] = useState<Group | null>(null);
  const [formOpen, setFormOpen] = useState(false);
  const [draft, setDraft] = useState<Draft>(emptyDraft);
  const [busy, setBusy] = useState(false);
  const [deleting, setDeleting] = useState<Group | null>(null);
  const toast = useToast();

  const load = useCallback(() => {
    api.groups().then(setGroups).catch((e) => toast("error", errorText(e)));
    api.accessKeys().then(setKeys).catch(() => {});
  }, [toast]);

  useEffect(() => {
    load();
    api.buckets().then(setBuckets).catch(() => {});
  }, [load]);

  const keyName = (ak: string) => keys.find((k) => k.accessKey === ak)?.name || ak;

  function openCreate() {
    setEditing(null);
    setDraft(emptyDraft);
    setFormOpen(true);
  }

  function openEdit(g: Group) {
    setEditing(g);
    setDraft({ name: g.name, description: g.description, buckets: g.buckets, readOnly: g.readOnly, members: g.members });
    setFormOpen(true);
  }

  const nameError = !editing && draft.name && !NAME_RULE.test(draft.name)
    ? "Use 1-64 letters, digits, dots, underscores and hyphens, starting with a letter or digit."
    : !editing && !draft.name.trim()
    ? "The group needs a name."
    : undefined;

  async function submit() {
    if (nameError) return;
    setBusy(true);
    try {
      if (editing) {
        await api.updateGroup(editing.name, {
          description: draft.description,
          buckets: draft.buckets,
          readOnly: draft.readOnly,
          members: draft.members,
        });
        toast("ok", `Group ${editing.name} updated.`);
      } else {
        await api.createGroup({ ...draft, name: draft.name.trim() });
        toast("ok", `Group ${draft.name.trim()} created.`);
      }
      setFormOpen(false);
      load();
    } catch (e) {
      toast("error", errorText(e));
    } finally {
      setBusy(false);
    }
  }

  async function setEnabled(g: Group, enabled: boolean) {
    try {
      await api.updateGroup(g.name, { enabled });
      toast("ok", `Group ${g.name} ${enabled ? "enabled" : "disabled"}.`);
      load();
    } catch (e) {
      toast("error", errorText(e));
    }
  }

  async function remove() {
    if (!deleting) return;
    setBusy(true);
    try {
      await api.deleteGroup(deleting.name);
      toast("ok", `Group ${deleting.name} deleted.`);
      setDeleting(null);
      load();
    } catch (e) {
      toast("error", errorText(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <PageHeader
        title="Groups"
        subtitle="Give several access keys the same permissions. A key in a group gets its access from its enabled groups instead of its own scope."
        actions={
          <Button variant="primary" icon={<Plus className="size-4" />} onClick={openCreate}>
            Create group
          </Button>
        }
      />

      {groups === null ? (
        <div className="flex justify-center py-20">
          <Spinner />
        </div>
      ) : groups.length === 0 ? (
        <div className="rounded-card border border-dashed border-line-strong bg-surface">
          <EmptyState
            title="No groups"
            body="Create a group per team or application, choose the buckets it may use, and add access keys to it."
            action={
              <Button variant="primary" icon={<UsersIcon className="size-4" />} onClick={openCreate}>
                Create your first group
              </Button>
            }
          />
        </div>
      ) : (
        <div className="overflow-hidden rounded-card border border-line bg-surface">
          <table className="w-full text-sm">
            <thead className="border-b border-line bg-sunken/60 text-left">
              <tr className="label-strip">
                <th className="w-full py-2 pr-3 pl-4 font-normal md:w-[32%]">group</th>
                <th className="hidden py-2 pr-3 font-normal md:table-cell">permissions</th>
                <th className="hidden py-2 pr-3 font-normal sm:table-cell">members</th>
                <th className="hidden py-2 pr-3 font-normal lg:table-cell">created</th>
                <th className="py-2 pr-4 font-normal">
                  <span className="sr-only">actions</span>
                </th>
              </tr>
            </thead>
            <tbody className="divide-y divide-line">
              {groups.map((g) => (
                <tr key={g.name} className={cx(!g.enabled && "text-muted")}>
                  <td className="max-w-0 py-3 pr-3 pl-4">
                    <div className="flex items-center gap-2">
                      <span className="truncate font-medium">{g.name}</span>
                      {!g.enabled && <Badge>disabled</Badge>}
                    </div>
                    {g.description && <div className="mt-0.5 truncate text-xs text-muted">{g.description}</div>}
                  </td>
                  <td className="hidden py-3 pr-3 md:table-cell">
                    <div className="flex flex-wrap items-center gap-1">
                      {g.buckets.length === 0 ? (
                        <span className="text-xs">All buckets</span>
                      ) : (
                        g.buckets.map((b) => (
                          <span key={b} className="rounded bg-sunken px-1.5 py-0.5 font-mono text-[11px]">
                            {b}
                          </span>
                        ))
                      )}
                      {g.readOnly && <Badge tone="accent">read-only</Badge>}
                    </div>
                  </td>
                  <td className="hidden py-3 pr-3 sm:table-cell">
                    {g.members.length === 0 ? (
                      <span className="text-xs text-faint">No members</span>
                    ) : (
                      <div className="flex flex-wrap gap-1" title={g.members.map(keyName).join(", ")}>
                        {g.members.slice(0, 3).map((m) => (
                          <span key={m} className="rounded bg-sunken px-1.5 py-0.5 text-[11px]">
                            {keyName(m)}
                          </span>
                        ))}
                        {g.members.length > 3 && <span className="text-xs text-muted">+{g.members.length - 3}</span>}
                      </div>
                    )}
                  </td>
                  <td className="hidden py-3 pr-3 text-xs lg:table-cell">{date(g.createdAt)}</td>
                  <td className="py-3 pr-3">
                    <div className="flex items-center justify-end gap-1">
                      <label className="relative mr-1 inline-flex cursor-pointer items-center" title={g.enabled ? "Disable" : "Enable"}>
                        <input
                          type="checkbox"
                          name="enabled"
                          className="peer sr-only"
                          checked={g.enabled}
                          onChange={(e) => setEnabled(g, e.target.checked)}
                          aria-label={`Enabled ${g.name}`}
                        />
                        <span className="h-5 w-9 rounded-full bg-line-strong transition-colors peer-checked:bg-ok peer-focus-visible:ring-2 peer-focus-visible:ring-accent/40" />
                        <span className="absolute left-0.5 size-4 rounded-full bg-surface shadow transition-transform peer-checked:translate-x-4" />
                      </label>
                      <IconButton label="Edit" onClick={() => openEdit(g)}>
                        <Pencil className="size-4" />
                      </IconButton>
                      <IconButton label="Delete" onClick={() => setDeleting(g)} className="hover:text-danger">
                        <Trash2 className="size-4" />
                      </IconButton>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <Modal
        open={formOpen}
        onClose={() => setFormOpen(false)}
        size="lg"
        title={
          <span className="flex items-center gap-2">
            <UsersIcon className="size-4 text-accent" />
            {editing ? `Edit group ${editing.name}` : "Create group"}
          </span>
        }
        footer={
          <>
            <Button onClick={() => setFormOpen(false)}>Cancel</Button>
            <Button variant="primary" busy={busy} disabled={!!nameError} onClick={submit}>
              {editing ? "Save" : "Create"}
            </Button>
          </>
        }
      >
        <form
          className="space-y-5"
          onSubmit={(e) => {
            e.preventDefault();
            submit();
          }}
        >
          {!editing && (
            <Field label="Name" error={nameError} hint="Used in the audit log; it cannot change later.">
              <Input
                autoFocus
                name="name"
                value={draft.name}
                onChange={(e) => setDraft({ ...draft, name: e.target.value })}
                placeholder="e.g. analytics-team"
                mono
              />
            </Field>
          )}
          <Field label="Description">
            <Input
              name="description"
              value={draft.description}
              onChange={(e) => setDraft({ ...draft, description: e.target.value })}
              placeholder="What the group is for"
            />
          </Field>
          <div>
            <span className="mb-1.5 block text-sm font-medium text-ink">Buckets</span>
            <MultiSelect
              aria-label="Buckets"
              values={draft.buckets}
              onChange={(buckets) => setDraft({ ...draft, buckets })}
              options={buckets.map((b) => ({ value: b.name, label: b.name }))}
              noneLabel="All buckets"
              mono
            />
          </div>
          <label className="flex items-center gap-2 text-sm">
            <input
              type="checkbox"
              name="read-only"
              checked={draft.readOnly}
              onChange={(e) => setDraft({ ...draft, readOnly: e.target.checked })}
              className="size-4 accent-[var(--accent)]"
            />
            Read-only (no uploads, deletes or bucket changes)
          </label>
          <div>
            <span className="mb-1.5 block text-sm font-medium text-ink">Members</span>
            <MultiSelect
              aria-label="Members"
              values={draft.members}
              onChange={(members) => setDraft({ ...draft, members })}
              options={keys.map((k) => ({ value: k.accessKey, label: k.name || k.accessKey, hint: k.accessKey.slice(0, 8) + "…" }))}
              noneLabel="No members"
              emptyText="No access keys match"
            />
            <p className="mt-1.5 text-xs text-muted">
              Members stop using their own bucket list and read-only setting while they belong to a group.
            </p>
          </div>
        </form>
      </Modal>

      <Modal
        open={!!deleting}
        onClose={() => setDeleting(null)}
        title="Delete group"
        footer={
          <>
            <Button onClick={() => setDeleting(null)}>Cancel</Button>
            <Button variant="danger" busy={busy} onClick={remove}>
              Delete
            </Button>
          </>
        }
      >
        <p className="text-sm">
          Delete <span className="font-mono font-semibold">{deleting?.name}</span>? Its {deleting?.members.length ?? 0} member key(s) go back to
          their own permissions.
        </p>
      </Modal>
    </>
  );
}
