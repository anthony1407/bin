import { useState, type FormEvent } from "react";
import { KeyRound } from "lucide-react";
import { useAuth } from "../auth";
import { api } from "../api";
import { Button, Card, Field, Input, PageHeader, errorText, useToast } from "../components/ui";

const MIN_PASSWORD = 10;

/** A user's own account: who they are, and changing their console password. */
export function AccountPage() {
  const { session } = useAuth();
  const toast = useToast();
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [repeat, setRepeat] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  async function submit(e: FormEvent) {
    e.preventDefault();
    if ([...next].length < MIN_PASSWORD) {
      setError(`Choose a password of at least ${MIN_PASSWORD} characters.`);
      return;
    }
    if (next !== repeat) {
      setError("The two new passwords do not match.");
      return;
    }
    setBusy(true);
    setError("");
    try {
      await api.changeOwnPassword(current, next);
      setCurrent("");
      setNext("");
      setRepeat("");
      toast("ok", "Password changed. Your other sessions were signed out.");
    } catch (err) {
      setError(errorText(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="max-w-xl space-y-6">
      <PageHeader title="My Account" subtitle="Your console sign-in. Your apps keep using your access key and secret key." />
      <Card className="space-y-1 p-5">
        <div className="label-strip">Access key</div>
        <div className="font-mono text-sm font-semibold text-ink">{session?.user}</div>
        <p className="pt-2 text-xs text-muted">
          You see the buckets your account may use.
          {session?.canCreateBuckets ? " You may also create buckets; the ones you create are yours." : ""}
        </p>
      </Card>
      <Card className="p-5">
        <form onSubmit={submit} className="space-y-4">
          <h2 className="flex items-center gap-2 text-sm font-semibold">
            <KeyRound className="size-4 text-accent" /> Change password
          </h2>
          <Field label="Current password">
            <Input type="password" autoComplete="current-password" value={current} onChange={(e) => setCurrent(e.target.value)} required />
          </Field>
          <Field label="New password" hint={`At least ${MIN_PASSWORD} characters.`}>
            <Input type="password" autoComplete="new-password" value={next} onChange={(e) => setNext(e.target.value)} required />
          </Field>
          <Field label="Repeat the new password">
            <Input type="password" autoComplete="new-password" value={repeat} onChange={(e) => setRepeat(e.target.value)} required />
          </Field>
          {error && <div className="rounded-md border border-danger/30 bg-danger-soft px-3 py-2 text-sm text-danger">{error}</div>}
          <Button type="submit" variant="primary" busy={busy}>
            {busy ? "Saving…" : "Change password"}
          </Button>
        </form>
      </Card>
    </div>
  );
}
