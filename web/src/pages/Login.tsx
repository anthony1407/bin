import { useEffect, useState, type FormEvent } from "react";
import { useLocation } from "react-router";
import { ArrowRight, Eye, EyeOff, KeyRound } from "lucide-react";
import { useAuth } from "../auth";
import { api, ApiError, type LoginProviders } from "../api";
import { BinLogo, BinMark, Button, Field, Input, PasswordInput, cx } from "../components/ui";
import { PasswordRules } from "../components/PasswordRules";
import { passwordProblem } from "../lib/password";

// "key": root or an access key's user with their console password.
// "activate": a user proves access key + secret key once and picks a password.
type Mode = "key" | "ldap" | "activate";


export function LoginPage() {
  const { signIn, signInLDAP, activate } = useAuth();
  const location = useLocation();
  const params = new URLSearchParams(location.search);
  const nextParam = params.get("next");
  const next = nextParam && nextParam.startsWith("/") && !nextParam.startsWith("//") ? nextParam : "/buckets";
  const [providers, setProviders] = useState<LoginProviders>({ openid: null, ldap: false });
  const [mode, setMode] = useState<Mode>("key");
  const [accessKey, setAccessKey] = useState("");
  const [secretKey, setSecretKey] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  // Set once the server answers that this account needs its authenticator code.
  const [needCode, setNeedCode] = useState(false);
  const [code, setCode] = useState("");
  const [show, setShow] = useState(false);
  const [busy, setBusy] = useState(false);
  // An OpenID sign-in that failed comes back here with the reason.
  const [error, setError] = useState(params.get("error") ?? "");

  useEffect(() => {
    api.loginProviders().then(setProviders).catch(() => {});
  }, []);

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (busy) return;
    if (mode === "activate") {
      if (passwordProblem(newPassword)) {
        setError(passwordProblem(newPassword));
        return;
      }
      if (newPassword !== confirmPassword) {
        setError("The two passwords do not match.");
        return;
      }
    }
    setBusy(true);
    setError("");
    const totp = needCode ? code : undefined;
    try {
      if (mode === "ldap") await signInLDAP(accessKey, secretKey, next);
      else if (mode === "activate") await activate(accessKey, secretKey, newPassword, next, totp);
      else await signIn(accessKey, secretKey, next, totp);
    } catch (err) {
      if (err instanceof ApiError && err.code === "totp_required") {
        setNeedCode(true); // the password was right: the code comes next
        setError("");
      } else {
        setError(err instanceof ApiError ? err.message : "Cannot reach the server.");
      }
      setBusy(false);
    }
  }

  function switchMode(m: Mode) {
    setMode(m);
    setError("");
    setSecretKey("");
    setNewPassword("");
    setConfirmPassword("");
    setNeedCode(false);
    setCode("");
  }

  const ldap = mode === "ldap";
  const activating = mode === "activate";
  // What the second field holds: a console password, or the S3 secret key while activating.
  const secretLabel = activating ? "Secret key" : "Password";

  return (
    <div className="grid min-h-dvh lg:grid-cols-[1.1fr_1fr]">
      {/* The brand panel is night-coloured in both themes, like the logo on dark. */}
      <section className="grid-paper relative hidden flex-col justify-between overflow-hidden bg-night p-12 text-slate-100 lg:flex">
        <BinLogo className="h-10 w-auto self-start" letters="text-white" />
        <BinMark className="pointer-events-none absolute -right-24 -bottom-28 size-[30rem] text-accent opacity-15" />
        <div className="relative max-w-md">
          <p className="label-strip mb-4 text-slate-400">single-node · s3-compatible · self-hosted</p>
          <h1 className="text-4xl leading-tight font-semibold tracking-tight text-white">
            Your files, in a bin
            <br />
            you actually own.
          </h1>
          <p className="mt-4 text-base text-slate-400">
            Buckets, objects and access keys for your own S3-compatible storage. Works with aws-cli, rclone and every S3 SDK.
          </p>
        </div>
        <div className="relative flex gap-8 font-mono text-xs text-slate-300">
          <div>
            <div className="label-strip mb-1 text-slate-500">s3 api</div>:9000
          </div>
          <div>
            <div className="label-strip mb-1 text-slate-500">console</div>:9001
          </div>
        </div>
      </section>

      <section className="flex items-center justify-center px-5 py-12">
        <div className="w-full max-w-sm">
          <BinLogo className="mb-10 h-9 w-auto lg:hidden" />
          <h2 className="text-2xl font-semibold tracking-tight">{activating ? "Set your console password" : "Sign in"}</h2>
          <p className="mt-1.5 text-sm text-muted">
            {ldap
              ? "Use your directory username and password."
              : activating
                ? "Prove your account once with your access key and secret key, then choose the password you will sign in with. Your secret key stays the same for your apps."
                : "Use your access key and console password. Root uses the credentials this server was started with."}
          </p>

          {providers.ldap && !activating && (
            <div className="mt-6 grid grid-cols-2 rounded-md border border-line p-0.5 text-sm" role="tablist">
              {(["key", "ldap"] as const).map((m) => (
                <button
                  key={m}
                  type="button"
                  role="tab"
                  aria-selected={mode === m}
                  onClick={() => switchMode(m)}
                  className={cx(
                    "rounded px-3 py-1.5 font-medium transition-colors",
                    mode === m ? "bg-accent-soft text-accent-ink" : "text-muted hover:text-ink",
                  )}
                >
                  {m === "key" ? "Access key" : "LDAP"}
                </button>
              ))}
            </div>
          )}

          <form onSubmit={submit} className={cx("space-y-5", providers.ldap && !activating ? "mt-6" : "mt-8")}>
            <Field label={ldap ? "Username" : "Access key"}>
              <Input mono={!ldap} autoFocus autoComplete="username" value={accessKey} onChange={(e) => setAccessKey(e.target.value)} readOnly={busy} required />
            </Field>
            <Field label={secretLabel}>
              <div className="relative">
                <Input
                  mono={activating}
                  readOnly={busy}
                  type={show ? "text" : "password"}
                  autoComplete={activating ? "off" : "current-password"}
                  value={secretKey}
                  onChange={(e) => setSecretKey(e.target.value)}
                  required
                  className="pr-10"
                />
                <button
                  type="button"
                  onClick={() => setShow(!show)}
                  className="absolute inset-y-0 right-0 flex w-10 items-center justify-center text-faint hover:text-ink"
                  aria-label={`${show ? "Hide" : "Show"} ${secretLabel.toLowerCase()}`}
                >
                  {show ? <EyeOff className="size-4" /> : <Eye className="size-4" />}
                </button>
              </div>
            </Field>
            {activating && (
              <>
                <Field label="New console password">
                  <PasswordInput
                    autoComplete="new-password"
                    value={newPassword}
                    onChange={(e) => setNewPassword(e.target.value)}
                    readOnly={busy}
                    required
                  />
                  <PasswordRules password={newPassword} />
                </Field>
                <Field
                  label="Confirm the new password"
                  error={confirmPassword && confirmPassword !== newPassword ? "The passwords do not match." : undefined}
                >
                  <PasswordInput
                    autoComplete="new-password"
                    value={confirmPassword}
                    onChange={(e) => setConfirmPassword(e.target.value)}
                    readOnly={busy}
                    required
                  />
                </Field>
              </>
            )}
            {needCode && (
              <Field label="Authenticator code" hint="The six digits your authenticator app shows for bin.">
                <Input
                  mono
                  autoFocus
                  inputMode="numeric"
                  autoComplete="one-time-code"
                  maxLength={6}
                  value={code}
                  onChange={(e) => setCode(e.target.value.replace(/\D/g, ""))}
                  readOnly={busy}
                  required
                />
              </Field>
            )}
            {error && <div className="rounded-md border border-danger/30 bg-danger-soft px-3 py-2 text-sm text-danger">{error}</div>}
            <Button type="submit" variant="primary" busy={busy} className="w-full disabled:opacity-90" icon={<ArrowRight className="size-4" />}>
              {busy ? (activating ? "Saving…" : "Signing in…") : activating ? "Save password and sign in" : "Sign in"}
            </Button>
          </form>

          {!ldap && (
            <button
              type="button"
              onClick={() => switchMode(activating ? "key" : "activate")}
              className="mt-4 w-full text-center text-sm text-accent hover:underline"
            >
              {activating ? "Back to sign in" : "First time, or forgot your password? Set it with your secret key"}
            </button>
          )}

          {providers.openid && !activating && (
            <>
              <div className="my-6 flex items-center gap-3 text-xs text-faint">
                <span className="h-px flex-1 bg-line" />
                or
                <span className="h-px flex-1 bg-line" />
              </div>
              {/* A full navigation: the provider's page takes over, then sends the browser back. */}
              <a
                href={api.openIDLoginURL(next)}
                className="flex w-full items-center justify-center gap-2 rounded-md border border-line px-4 py-2 text-sm font-medium text-ink transition-colors hover:bg-sunken"
              >
                <KeyRound className="size-4 text-accent" />
                Sign in with {providers.openid.displayName}
              </a>
            </>
          )}
        </div>
      </section>
    </div>
  );
}
