import { useEffect, useState, type ReactNode } from "react";
import { CheckCircle2, Info, KeyRound, LogIn, PlugZap, Save } from "lucide-react";
import { api, ApiError, type LDAPConfig, type OpenIDConfig } from "../api";
import { Badge, Button, Card, Field, Input, PageHeader, Spinner, errorText, useToast } from "../components/ui";
import { Select } from "../components/Select";

// Console sign-in with an OpenID Connect provider or an LDAP directory. Only
// members of the configured admin group get in, with full access; root can
// always sign in, so a broken configuration cannot lock anyone out.

type Errors = Record<string, string>;

/**
 * Turns a failed request into field errors: the server names the field it
 * refused; anything else goes under the form ("" key).
 */
function requestErrors(e: unknown): Errors {
  if (e instanceof ApiError && typeof e.data.field === "string" && e.data.field) {
    return { [e.data.field]: e.message };
  }
  return { "": errorText(e) };
}

/** Errors for required fields left empty, keyed by field. */
function missing<T>(cfg: T, fields: (keyof T & string)[]): Errors {
  const out: Errors = {};
  for (const f of fields) {
    if (!String(cfg[f] ?? "").trim()) out[f] = "Required to enable sign-in.";
  }
  return out;
}

function EnabledSwitch({ enabled, onChange }: { enabled: boolean; onChange: (v: boolean) => void }) {
  return (
    <div className="space-y-1">
      <label className="flex cursor-pointer items-center gap-3">
        <input type="checkbox" className="size-4 accent-[var(--accent)]" checked={enabled} onChange={(e) => onChange(e.target.checked)} />
        <span className="text-sm font-medium text-ink">Enable sign-in</span>
        <Badge tone={enabled ? "ok" : "neutral"}>{enabled ? "on" : "off"}</Badge>
      </label>
      <p className="text-xs text-muted">
        Fields marked <span className="text-danger">*</span> are required to enable sign-in. While it is off, you can
        save an incomplete configuration.
      </p>
    </div>
  );
}

function SidePanel({ children }: { children: ReactNode }) {
  return (
    <Card className="space-y-3 p-5 text-xs leading-relaxed text-muted">
      <h3 className="flex items-center gap-2 text-sm font-semibold text-ink">
        <Info className="size-4 text-accent" />
        How it works
      </h3>
      {children}
    </Card>
  );
}

function TestResult({ result }: { result: { ok: boolean; text: string } | null }) {
  if (!result) return null;
  return (
    <div
      className={
        result.ok
          ? "flex items-start gap-2 rounded-md border border-ok/30 bg-ok-soft px-3 py-2 text-xs text-ok"
          : "rounded-md border border-danger/30 bg-danger-soft px-3 py-2 text-xs text-danger"
      }
    >
      {result.ok && <CheckCircle2 className="mt-0.5 size-3.5 shrink-0" />}
      <span className="break-all">{result.text}</span>
    </div>
  );
}

/** Form errors that belong to no single field. */
function FormError({ text }: { text?: string }) {
  if (!text) return null;
  return <div className="rounded-md border border-danger/30 bg-danger-soft px-3 py-2 text-xs text-danger">{text}</div>;
}

// --- OpenID Connect ---------------------------------------------------------------------

const OPENID_REQUIRED: (keyof OpenIDConfig & string)[] = ["configUrl", "clientId", "claimName", "adminValue", "scopes"];

function validateOpenID(c: OpenIDConfig): Errors {
  const errs = c.enabled ? missing(c, OPENID_REQUIRED) : {};
  if (c.configUrl.trim() && !/^https?:\/\/[^/]+.*\/\.well-known\/openid-configuration$/.test(c.configUrl.trim())) {
    errs.configUrl = "Use the provider's discovery address, ending in /.well-known/openid-configuration.";
  }
  if (c.scopes.trim() && !c.scopes.split(/\s+/).includes("openid")) {
    errs.scopes = "The scopes must include openid.";
  }
  if (c.redirectUri.trim() && !/^https?:\/\/[^/]+/.test(c.redirectUri.trim())) {
    errs.redirectUri = "Use an http:// or https:// URL.";
  }
  return errs;
}

export function OpenIDPage() {
  const [cfg, setCfg] = useState<OpenIDConfig | null>(null);
  const [secret, setSecret] = useState("");
  const [errors, setErrors] = useState<Errors>({});
  const [saving, setSaving] = useState(false);
  const [testing, setTesting] = useState(false);
  const [test, setTest] = useState<{ ok: boolean; text: string } | null>(null);
  const toast = useToast();

  useEffect(() => {
    api.openIDConfig().then(setCfg).catch((e) => setErrors(requestErrors(e)));
  }, []);

  if (!cfg) {
    return errors[""] ? (
      <FormError text={errors[""]} />
    ) : (
      <div className="flex justify-center py-16">
        <Spinner />
      </div>
    );
  }
  const set = <K extends keyof OpenIDConfig>(k: K, v: OpenIDConfig[K]) => {
    setCfg({ ...cfg, [k]: v });
    setErrors(({ [k]: _, "": __, ...rest }) => rest);
  };

  async function save() {
    const errs = validateOpenID(cfg!);
    setErrors(errs);
    if (Object.keys(errs).length > 0) return;
    setSaving(true);
    try {
      setCfg(await api.saveOpenIDConfig({ ...cfg!, ...(secret ? { clientSecret: secret } : {}) }));
      setSecret("");
      toast("ok", "OpenID configuration saved.");
    } catch (e) {
      setErrors(requestErrors(e));
    } finally {
      setSaving(false);
    }
  }

  async function runTest() {
    setTest(null);
    if (!cfg!.configUrl.trim()) {
      setErrors({ ...errors, configUrl: "Enter the configuration URL to test the provider." });
      return;
    }
    setTesting(true);
    try {
      const r = await api.testOpenID(cfg!.configUrl);
      setTest({ ok: true, text: `Provider found: ${r.issuer}` + (r.claimsSupported?.length ? ` · claims: ${r.claimsSupported.join(", ")}` : "") });
    } catch (e) {
      const errs = requestErrors(e);
      if (errs[""]) setTest({ ok: false, text: errs[""] });
      else setErrors({ ...errors, ...errs });
    } finally {
      setTesting(false);
    }
  }

  return (
    <>
      <PageHeader
        title={
          <span className="flex items-center gap-2.5">
            <KeyRound className="size-6 text-accent" />
            OpenID Connect
          </span>
        }
        subtitle="Sign in to the console with an identity provider such as Keycloak, Google Workspace, Microsoft Entra ID or Okta."
      />
      <div className="grid grid-cols-1 gap-6 lg:grid-cols-3">
        <Card className="space-y-5 p-5 lg:col-span-2">
          <EnabledSwitch enabled={cfg.enabled} onChange={(v) => set("enabled", v)} />
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <div className="sm:col-span-2">
              <Field label="Configuration URL" required hint="The provider's discovery document." error={errors.configUrl}>
                <Input
                  value={cfg.configUrl}
                  aria-invalid={!!errors.configUrl}
                  onChange={(e) => set("configUrl", e.target.value)}
                  placeholder="https://idp.example.com/.well-known/openid-configuration"
                />
              </Field>
            </div>
            <Field label="Client ID" required error={errors.clientId}>
              <Input value={cfg.clientId} aria-invalid={!!errors.clientId} onChange={(e) => set("clientId", e.target.value)} placeholder="bin-console" />
            </Field>
            <Field label="Client Secret" hint={cfg.clientSecretSet ? "Stored. Leave blank to keep it." : "Leave blank for a public client."}>
              <Input
                type="password"
                autoComplete="new-password"
                value={secret}
                onChange={(e) => setSecret(e.target.value)}
                placeholder={cfg.clientSecretSet ? "••••••••" : ""}
              />
            </Field>
            <Field label="Claim Name" required hint="Claim listing the user's groups or roles." error={errors.claimName}>
              <Input value={cfg.claimName} aria-invalid={!!errors.claimName} onChange={(e) => set("claimName", e.target.value)} placeholder="groups" />
            </Field>
            <Field label="Admin Claim Value" required hint="Users whose claim contains it can sign in." error={errors.adminValue}>
              <Input value={cfg.adminValue} aria-invalid={!!errors.adminValue} onChange={(e) => set("adminValue", e.target.value)} placeholder="bin-admins" />
            </Field>
            <Field label="Display Name" hint="Shown on the sign-in button.">
              <Input value={cfg.displayName} onChange={(e) => set("displayName", e.target.value)} placeholder="SSO" />
            </Field>
            <Field label="Scopes" required hint="Space separated; must include openid." error={errors.scopes}>
              <Input value={cfg.scopes} aria-invalid={!!errors.scopes} onChange={(e) => set("scopes", e.target.value)} placeholder="openid profile email" />
            </Field>
            <div className="sm:col-span-2">
              <Field
                label="Redirect URI"
                hint={`Register this at the provider. Leave blank to use ${cfg.defaultRedirectUri}`}
                error={errors.redirectUri}
              >
                <Input
                  value={cfg.redirectUri}
                  aria-invalid={!!errors.redirectUri}
                  onChange={(e) => set("redirectUri", e.target.value)}
                  placeholder={cfg.defaultRedirectUri}
                />
              </Field>
            </div>
          </div>
          <TestResult result={test} />
          <FormError text={errors[""]} />
          <div className="flex flex-wrap justify-end gap-2 border-t border-line pt-4">
            <Button icon={<PlugZap className="size-4" />} busy={testing} onClick={runTest}>
              Test Provider
            </Button>
            <Button variant="primary" icon={<Save className="size-4" />} busy={saving} onClick={save}>
              Save
            </Button>
          </div>
        </Card>
        <SidePanel>
          <p>
            Create a confidential client at your provider with the redirect URI shown here, and add a mapper so the
            ID token carries the users' groups in the claim above.
          </p>
          <p>
            Only users whose claim contains the admin value can sign in, and they get full access to the console, like
            root. Everyone else is turned away at the login page.
          </p>
          <p>Root can always sign in with its credentials, so a wrong setting here cannot lock you out.</p>
        </SidePanel>
      </div>
    </>
  );
}

// --- LDAP ------------------------------------------------------------------------------------

const LDAP_REQUIRED: (keyof LDAPConfig & string)[] = [
  "serverAddr",
  "userDnSearchBase",
  "userDnSearchFilter",
  "groupSearchBase",
  "groupSearchFilter",
  "adminGroupDn",
];

function validateLDAP(c: LDAPConfig): Errors {
  const errs = c.enabled ? missing(c, LDAP_REQUIRED) : {};
  if (c.serverAddr.trim() && !/^[^\s:]+:\d+$|^\[[^\]]+\]:\d+$/.test(c.serverAddr.trim())) {
    errs.serverAddr = "Use host:port, e.g. ldap.example.com:636.";
  }
  if (c.userDnSearchFilter.trim() && !c.userDnSearchFilter.includes("%s")) {
    errs.userDnSearchFilter = "The filter must contain %s, replaced by the username.";
  }
  if (c.groupSearchFilter.trim() && !/%[ds]/.test(c.groupSearchFilter)) {
    errs.groupSearchFilter = "The filter must contain %d (the user's DN) or %s (the username).";
  }
  return errs;
}

export function LDAPPage() {
  const [cfg, setCfg] = useState<LDAPConfig | null>(null);
  const [password, setPassword] = useState("");
  const [errors, setErrors] = useState<Errors>({});
  const [saving, setSaving] = useState(false);
  const [testing, setTesting] = useState(false);
  const [test, setTest] = useState<{ ok: boolean; text: string } | null>(null);
  const toast = useToast();

  useEffect(() => {
    api.ldapConfig().then(setCfg).catch((e) => setErrors(requestErrors(e)));
  }, []);

  if (!cfg) {
    return errors[""] ? (
      <FormError text={errors[""]} />
    ) : (
      <div className="flex justify-center py-16">
        <Spinner />
      </div>
    );
  }
  const set = <K extends keyof LDAPConfig>(k: K, v: LDAPConfig[K]) => {
    setCfg({ ...cfg, [k]: v });
    setErrors(({ [k]: _, "": __, ...rest }) => rest);
  };
  const body = () => ({ ...cfg!, ...(password ? { lookupBindPassword: password } : {}) });

  async function save() {
    const errs = validateLDAP(cfg!);
    setErrors(errs);
    if (Object.keys(errs).length > 0) return;
    setSaving(true);
    try {
      setCfg(await api.saveLDAPConfig(body()));
      setPassword("");
      toast("ok", "LDAP configuration saved.");
    } catch (e) {
      setErrors(requestErrors(e));
    } finally {
      setSaving(false);
    }
  }

  async function runTest() {
    setTest(null);
    // Tested as typed, even before saving; it need not be complete to be tested.
    const errs = validateLDAP({ ...cfg!, enabled: false });
    if (!cfg!.serverAddr.trim()) errs.serverAddr = "Enter the server address to test the connection.";
    if (Object.keys(errs).length > 0) {
      setErrors(errs);
      return;
    }
    setTesting(true);
    try {
      await api.testLDAP({ ...body(), enabled: false });
      setTest({ ok: true, text: `Connected to ${cfg!.serverAddr} and bound with the lookup account.` });
    } catch (e) {
      const errs = requestErrors(e);
      if (errs[""]) setTest({ ok: false, text: errs[""] });
      else setErrors({ ...errors, ...errs });
    } finally {
      setTesting(false);
    }
  }

  return (
    <>
      <PageHeader
        title={
          <span className="flex items-center gap-2.5">
            <LogIn className="size-6 text-accent" />
            LDAP / Active Directory
          </span>
        }
        subtitle="Sign in to the console with directory accounts. Members of the admin group get full access."
      />
      <div className="grid grid-cols-1 gap-6 lg:grid-cols-3">
        <Card className="space-y-5 p-5 lg:col-span-2">
          <EnabledSwitch enabled={cfg.enabled} onChange={(v) => set("enabled", v)} />
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <Field label="Server Address" required hint="host:port" error={errors.serverAddr}>
              <Input
                value={cfg.serverAddr}
                aria-invalid={!!errors.serverAddr}
                onChange={(e) => set("serverAddr", e.target.value)}
                placeholder="ldap.example.com:636"
              />
            </Field>
            <div>
              <label htmlFor="ldap-tls-mode" className="mb-1.5 block text-sm font-medium text-ink">
                Connection Security
              </label>
              <Select
                id="ldap-tls-mode"
                aria-label="Connection Security"
                value={cfg.tlsMode}
                onChange={(v) => set("tlsMode", v as LDAPConfig["tlsMode"])}
                options={[
                  { value: "tls", label: "TLS (ldaps://)" },
                  { value: "starttls", label: "StartTLS" },
                  { value: "none", label: "None (plain text)" },
                ]}
              />
              {errors.tlsMode && <span className="mt-1.5 block text-xs text-danger">{errors.tlsMode}</span>}
              <label className="mt-2 flex items-center gap-2 text-xs text-muted">
                <input
                  type="checkbox"
                  className="size-3.5 accent-[var(--accent)]"
                  checked={cfg.tlsSkipVerify}
                  disabled={cfg.tlsMode === "none"}
                  onChange={(e) => set("tlsSkipVerify", e.target.checked)}
                />
                Skip certificate verification (testing only)
              </label>
            </div>
            <Field label="Lookup Bind DN" hint="Leave blank to search anonymously." error={errors.lookupBindDn}>
              <Input
                value={cfg.lookupBindDn}
                aria-invalid={!!errors.lookupBindDn}
                onChange={(e) => set("lookupBindDn", e.target.value)}
                placeholder="cn=bin,ou=services,dc=example,dc=com"
              />
            </Field>
            <Field label="Lookup Bind Password" hint={cfg.lookupBindPasswordSet ? "Stored. Leave blank to keep it." : undefined}>
              <Input
                type="password"
                autoComplete="new-password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                placeholder={cfg.lookupBindPasswordSet ? "••••••••" : ""}
              />
            </Field>
            <Field label="User DN Search Base" required error={errors.userDnSearchBase}>
              <Input
                value={cfg.userDnSearchBase}
                aria-invalid={!!errors.userDnSearchBase}
                onChange={(e) => set("userDnSearchBase", e.target.value)}
                placeholder="ou=people,dc=example,dc=com"
              />
            </Field>
            <Field label="User DN Search Filter" required hint="%s is the username." error={errors.userDnSearchFilter}>
              <Input
                mono
                value={cfg.userDnSearchFilter}
                aria-invalid={!!errors.userDnSearchFilter}
                onChange={(e) => set("userDnSearchFilter", e.target.value)}
                placeholder="(uid=%s)"
              />
            </Field>
            <Field label="Group Search Base" required error={errors.groupSearchBase}>
              <Input
                value={cfg.groupSearchBase}
                aria-invalid={!!errors.groupSearchBase}
                onChange={(e) => set("groupSearchBase", e.target.value)}
                placeholder="ou=groups,dc=example,dc=com"
              />
            </Field>
            <Field label="Group Search Filter" required hint="%d is the user's DN, %s the username." error={errors.groupSearchFilter}>
              <Input
                mono
                value={cfg.groupSearchFilter}
                aria-invalid={!!errors.groupSearchFilter}
                onChange={(e) => set("groupSearchFilter", e.target.value)}
                placeholder="(&(objectclass=groupOfNames)(member=%d))"
              />
            </Field>
            <div className="sm:col-span-2">
              <Field label="Admin Group DN" required hint="Members of this group can sign in." error={errors.adminGroupDn}>
                <Input
                  value={cfg.adminGroupDn}
                  aria-invalid={!!errors.adminGroupDn}
                  onChange={(e) => set("adminGroupDn", e.target.value)}
                  placeholder="cn=bin-admins,ou=groups,dc=example,dc=com"
                />
              </Field>
            </div>
          </div>
          <TestResult result={test} />
          <FormError text={errors[""]} />
          <div className="flex flex-wrap justify-end gap-2 border-t border-line pt-4">
            <Button icon={<PlugZap className="size-4" />} busy={testing} onClick={runTest}>
              Test Connection
            </Button>
            <Button variant="primary" icon={<Save className="size-4" />} busy={saving} onClick={save}>
              Save
            </Button>
          </div>
        </Card>
        <SidePanel>
          <p>
            bin binds with the lookup account, finds the user with the user filter, checks the password by binding as
            that user, then looks up the user's groups.
          </p>
          <p>
            Only members of the admin group can sign in, and they get full access to the console, like root. On the
            login page they choose <span className="font-medium text-ink">LDAP</span> and use their directory username.
          </p>
          <p>Root can always sign in with its credentials, so a wrong setting here cannot lock you out.</p>
        </SidePanel>
      </div>
    </>
  );
}
