import { useState, type FormEvent } from "react";
import { useLocation } from "react-router";
import { ArrowRight, Eye, EyeOff } from "lucide-react";
import { useAuth } from "../auth";
import { ApiError } from "../api";
import { BinLogo, BinMark, Button, Field, Input } from "../components/ui";

export function LoginPage() {
  const { signIn } = useAuth();
  const location = useLocation();
  const [accessKey, setAccessKey] = useState("");
  const [secretKey, setSecretKey] = useState("");
  const [show, setShow] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (busy) return;
    setBusy(true);
    setError("");
    const next = new URLSearchParams(location.search).get("next");
    try {
      await signIn(accessKey, secretKey, next && next.startsWith("/") && !next.startsWith("//") ? next : "/buckets");
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Cannot reach the server.");
      setBusy(false);
    }
  }

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
          <h2 className="text-2xl font-semibold tracking-tight">Sign in</h2>
          <p className="mt-1.5 text-sm text-muted">Use the root credentials this server was started with.</p>

          <form onSubmit={submit} className="mt-8 space-y-5">
            <Field label="Access key">
              <Input mono autoFocus autoComplete="username" value={accessKey} onChange={(e) => setAccessKey(e.target.value)} readOnly={busy} required />
            </Field>
            <Field label="Secret key">
              <div className="relative">
                <Input
                  mono
                  readOnly={busy}
                  type={show ? "text" : "password"}
                  autoComplete="current-password"
                  value={secretKey}
                  onChange={(e) => setSecretKey(e.target.value)}
                  required
                  className="pr-10"
                />
                <button
                  type="button"
                  onClick={() => setShow(!show)}
                  className="absolute inset-y-0 right-0 flex w-10 items-center justify-center text-faint hover:text-ink"
                  aria-label={show ? "Hide secret key" : "Show secret key"}
                >
                  {show ? <EyeOff className="size-4" /> : <Eye className="size-4" />}
                </button>
              </div>
            </Field>
            {error && <div className="rounded-md border border-danger/30 bg-danger-soft px-3 py-2 text-sm text-danger">{error}</div>}
            <Button type="submit" variant="primary" busy={busy} className="w-full disabled:opacity-90" icon={<ArrowRight className="size-4" />}>
              {busy ? "Signing in…" : "Sign in"}
            </Button>
          </form>
          <p className="mt-8 text-xs text-faint">
            These are the values of <span className="font-mono">BIN_ROOT_ACCESS_KEY</span> and <span className="font-mono">BIN_ROOT_SECRET_KEY</span>.
          </p>
        </div>
      </section>
    </div>
  );
}
