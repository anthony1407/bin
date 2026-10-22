import type { ReactNode } from "react";
import { Clock, KeyRound, LogIn } from "lucide-react";
import { Badge, Card, Input, PageHeader } from "../components/ui";

// OpenID Connect and LDAP sign-in are planned: the console signs in with the
// root credentials only. These pages show what will be configurable, read-only.

function Planned({ fields, notes }: { fields: { label: string; placeholder: string }[]; notes: ReactNode }) {
  return (
    <div className="grid grid-cols-1 gap-6 lg:grid-cols-3">
      <Card className="space-y-4 p-5 lg:col-span-2">
        <div className="flex items-center gap-2">
          <Badge tone="warn">coming soon</Badge>
          <span className="text-xs text-muted">The fields below are a preview and cannot be saved yet.</span>
        </div>
        <fieldset disabled className="grid grid-cols-1 gap-4 opacity-70 sm:grid-cols-2">
          {fields.map((f) => (
            <label key={f.label} className="block">
              <span className="mb-1.5 block text-sm font-medium text-ink">{f.label}</span>
              <Input placeholder={f.placeholder} className="cursor-not-allowed" />
            </label>
          ))}
        </fieldset>
      </Card>
      <Card className="space-y-3 p-5 text-xs leading-relaxed text-muted">
        <h3 className="flex items-center gap-2 text-sm font-semibold text-ink">
          <Clock className="size-4 text-accent" />
          Until then
        </h3>
        {notes}
      </Card>
    </div>
  );
}

export function OpenIDPage() {
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
      <Planned
        fields={[
          { label: "Configuration URL", placeholder: "https://idp.example.com/.well-known/openid-configuration" },
          { label: "Client ID", placeholder: "bin-console" },
          { label: "Client Secret", placeholder: "••••••••" },
          { label: "Claim Name", placeholder: "groups" },
          { label: "Scopes", placeholder: "openid profile email" },
          { label: "Redirect URI", placeholder: "https://console.example.com/oauth/callback" },
        ]}
        notes={
          <>
            <p>The console signs in with the root credentials. Give people and apps their own access keys for S3 instead.</p>
            <p>When OpenID arrives, the claim above will map users to bin groups, so permissions stay managed in Identity → Groups.</p>
          </>
        }
      />
    </>
  );
}

export function LDAPPage() {
  return (
    <>
      <PageHeader
        title={
          <span className="flex items-center gap-2.5">
            <LogIn className="size-6 text-accent" />
            LDAP / Active Directory
          </span>
        }
        subtitle="Sign in to the console with directory accounts, and map directory groups to bin groups."
      />
      <Planned
        fields={[
          { label: "Server Address", placeholder: "ldap.example.com:636" },
          { label: "Lookup Bind DN", placeholder: "cn=bin,ou=services,dc=example,dc=com" },
          { label: "Lookup Bind Password", placeholder: "••••••••" },
          { label: "User DN Search Base", placeholder: "ou=people,dc=example,dc=com" },
          { label: "User DN Search Filter", placeholder: "(uid=%s)" },
          { label: "Group Search Base", placeholder: "ou=groups,dc=example,dc=com" },
        ]}
        notes={
          <>
            <p>The console signs in with the root credentials. Give people and apps their own access keys for S3 instead.</p>
            <p>When LDAP arrives, directory groups will map to bin groups, so permissions stay managed in Identity → Groups.</p>
          </>
        }
      />
    </>
  );
}
