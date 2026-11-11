import { useCallback, useEffect, useState, type FormEvent } from "react";
import { KeyRound, LogOut, ShieldCheck, Smartphone } from "lucide-react";
import { useAuth } from "../auth";
import { api, type SessionInfo } from "../api";
import { dateTime } from "../lib/format";
import { Button, Card, Field, Input, PageHeader, errorText, useToast } from "../components/ui";

const MIN_PASSWORD = 10;

/** A user's own account: their console password, second factor and sessions. */
export function AccountPage() {
  const { session } = useAuth();
  return (
    <div className="max-w-2xl space-y-6">
      <PageHeader title="My Account" subtitle="Your console sign-in. Your apps keep using your access key and secret key." />
      <Card className="space-y-1 p-5">
        <div className="label-strip">Access key</div>
        <div className="font-mono text-sm font-semibold text-ink">{session?.user}</div>
        <p className="pt-2 text-xs text-muted">
          You see the buckets your account may use.
          {session?.canCreateBuckets ? " You may also create buckets; the ones you create are yours." : ""}
        </p>
      </Card>
      <PasswordCard />
      <SecondFactorCard initiallyOn={!!session?.totp} />
      <SessionsCard />
    </div>
  );
}

function PasswordCard() {
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
  );
}

function SecondFactorCard({ initiallyOn }: { initiallyOn: boolean }) {
  const toast = useToast();
  const [on, setOn] = useState(initiallyOn);
  const [setup, setSetup] = useState<{ secret: string; url: string } | null>(null);
  const [code, setCode] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function run(action: () => Promise<void>) {
    setBusy(true);
    setError("");
    try {
      await action();
    } catch (err) {
      setError(errorText(err));
    } finally {
      setBusy(false);
    }
  }

  const start = () => run(async () => setSetup(await api.setupTOTP()));
  const enable = () =>
    run(async () => {
      await api.enableTOTP(code);
      setOn(true);
      setSetup(null);
      setCode("");
      toast("ok", "Second factor on. Signing in now also asks for a code; your other sessions were signed out.");
    });
  const disable = () =>
    run(async () => {
      await api.disableTOTP(code);
      setOn(false);
      setCode("");
      toast("ok", "Second factor off.");
    });

  const codeInput = (
    <Input
      mono
      inputMode="numeric"
      autoComplete="one-time-code"
      maxLength={6}
      placeholder="123456"
      value={code}
      onChange={(e) => setCode(e.target.value.replace(/\D/g, ""))}
      className="w-32"
    />
  );

  return (
    <Card className="space-y-4 p-5">
      <h2 className="flex items-center gap-2 text-sm font-semibold">
        <ShieldCheck className="size-4 text-accent" /> Second factor
        <span className={on ? "rounded bg-ok-soft px-1.5 py-0.5 text-[10px] text-ok" : "rounded bg-sunken px-1.5 py-0.5 text-[10px] text-muted"}>
          {on ? "on" : "off"}
        </span>
      </h2>
      {on ? (
        <>
          <p className="text-xs text-muted">
            Signing in asks for the code of your authenticator app. To turn it off, enter a current code. Lost the phone? An
            administrator can remove it.
          </p>
          <div className="flex items-center gap-2">
            {codeInput}
            <Button busy={busy} disabled={code.length !== 6} onClick={disable}>
              Turn off
            </Button>
          </div>
        </>
      ) : setup ? (
        <>
          <p className="text-xs text-muted">
            In your authenticator app (Google Authenticator, Microsoft Authenticator, 1Password…), add an account by entering
            this key, or open the link on your phone. Then type the code it shows.
          </p>
          <div className="rounded-md border border-line bg-sunken px-3 py-2 font-mono text-sm tracking-wider select-all break-all">
            {setup.secret.replace(/(.{4})/g, "$1 ").trim()}
          </div>
          <a href={setup.url} className="flex items-center gap-1.5 text-xs text-accent hover:underline">
            <Smartphone className="size-3.5" /> Open in the authenticator app
          </a>
          <div className="flex items-center gap-2">
            {codeInput}
            <Button variant="primary" busy={busy} disabled={code.length !== 6} onClick={enable}>
              Turn on
            </Button>
            <Button variant="ghost" onClick={() => setSetup(null)}>
              Cancel
            </Button>
          </div>
        </>
      ) : (
        <>
          <p className="text-xs text-muted">Add a code from an authenticator app to your password, so a leaked password alone cannot sign in.</p>
          <Button busy={busy} onClick={start}>
            Set up
          </Button>
        </>
      )}
      {error && <div className="rounded-md border border-danger/30 bg-danger-soft px-3 py-2 text-sm text-danger">{error}</div>}
    </Card>
  );
}

/** Live sessions; `accessKey` lists one user's (admins), otherwise the caller's own. */
export function SessionsList({ accessKey }: { accessKey?: string }) {
  const toast = useToast();
  const [list, setList] = useState<SessionInfo[] | null>(null);
  const load = useCallback(() => {
    api
      .sessions(accessKey)
      .then(setList)
      .catch((e) => toast("error", errorText(e)));
  }, [accessKey, toast]);
  useEffect(load, [load]);

  async function end(s: SessionInfo) {
    try {
      await api.endSession(s.id);
      toast("ok", "Session signed out.");
      load();
    } catch (e) {
      toast("error", errorText(e));
    }
  }

  if (!list) return <div className="text-xs text-muted">Loading…</div>;
  if (list.length === 0) return <div className="text-xs text-muted">No open sessions.</div>;
  return (
    <div className="divide-y divide-line rounded-md border border-line text-xs">
      {list.map((s) => (
        <div key={s.id} className="flex items-center justify-between gap-3 px-3 py-2">
          <div className="min-w-0">
            <div className="truncate font-medium text-ink" title={s.userAgent}>
              {browserName(s.userAgent)} {s.remote && <span className="font-mono text-muted">· {s.remote}</span>}
              {s.current && <span className="ml-1.5 rounded bg-accent-soft px-1 text-[10px] text-accent-ink">this one</span>}
            </div>
            <div className="text-muted">
              since {dateTime(s.createdAt)} · until {dateTime(s.expiresAt)}
            </div>
          </div>
          {!s.current && (
            <Button variant="ghost" icon={<LogOut className="size-3.5" />} onClick={() => end(s)}>
              Sign out
            </Button>
          )}
        </div>
      ))}
    </div>
  );
}

function SessionsCard() {
  return (
    <Card className="space-y-3 p-5">
      <h2 className="flex items-center gap-2 text-sm font-semibold">
        <LogOut className="size-4 text-accent" /> Open sessions
      </h2>
      <p className="text-xs text-muted">Where your account is signed in to the console. Sign out the ones you do not recognize.</p>
      <SessionsList />
    </Card>
  );
}

/** A short name for a User-Agent string. */
function browserName(ua?: string): string {
  if (!ua) return "Unknown browser";
  const os = /Windows/.test(ua) ? "Windows" : /Android/.test(ua) ? "Android" : /iPhone|iPad/.test(ua) ? "iOS" : /Mac OS/.test(ua) ? "macOS" : /Linux/.test(ua) ? "Linux" : "";
  const browser = /Edg\//.test(ua) ? "Edge" : /Chrome\//.test(ua) ? "Chrome" : /Firefox\//.test(ua) ? "Firefox" : /Safari\//.test(ua) ? "Safari" : ua.split(" ")[0];
  return os ? `${browser} on ${os}` : browser;
}
