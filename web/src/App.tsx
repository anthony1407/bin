import { Navigate, Route, Routes, useLocation } from "react-router";
import { AuthProvider, useAuth } from "./auth";
import { Layout } from "./components/Layout";
import { Spinner, ToastProvider } from "./components/ui";
import { LoginPage } from "./pages/Login";
import { BucketsPage } from "./pages/Buckets";
import { ExplorerPage } from "./pages/Explorer";
import { BucketSettingsPage } from "./pages/BucketSettings";
import { AccessKeysPage } from "./pages/AccessKeys";
import { SettingsPage } from "./pages/Settings";
import { PoliciesPage } from "./pages/Policies";
import { UsersPage } from "./pages/Users";
import { MetricsPage } from "./pages/Metrics";
import { LogsPage } from "./pages/Logs";
import { ConnectSDKPage } from "./pages/ConnectSDK";
import { SpeedtestPage } from "./pages/Speedtest";
import { DropzonePage, PublicUploadPage } from "./pages/Dropzone";
import { TerminalPage } from "./pages/Terminal";
import { ReplicationPage } from "./pages/Replication";
import { GroupsPage } from "./pages/Groups";
import { AuditPage } from "./pages/Audit";
import { LDAPPage, OpenIDPage } from "./pages/IdentityProviders";

function Routed() {
  const { session, loading } = useAuth();
  const location = useLocation();
  // Dropzone links are for people without an account: no sign-in, no panel.
  if (location.pathname === "/upload") {
    return <PublicUploadPage />;
  }
  if (loading) {
    return (
      <div className="flex min-h-dvh items-center justify-center">
        <Spinner />
      </div>
    );
  }
  if (!session) {
    return location.pathname === "/login" ? (
      <LoginPage />
    ) : (
      <Navigate to={`/login?next=${encodeURIComponent(location.pathname + location.search)}`} replace />
    );
  }
  if (location.pathname === "/login") {
    const next = new URLSearchParams(location.search).get("next");
    return <Navigate to={next && next.startsWith("/") && !next.startsWith("//") ? next : "/buckets"} replace />;
  }
  return (
    <Layout>
      <Routes>
        <Route path="/buckets" element={<BucketsPage />} />
        <Route path="/buckets/:bucket" element={<ExplorerPage />} />
        <Route path="/buckets/:bucket/settings" element={<BucketSettingsPage />} />
        <Route path="/access-keys" element={<AccessKeysPage />} />
        <Route path="/policies" element={<PoliciesPage />} />
        <Route path="/identity/users" element={<UsersPage />} />
        <Route path="/identity/groups" element={<GroupsPage />} />
        <Route path="/identity/openid" element={<OpenIDPage />} />
        <Route path="/identity/ldap" element={<LDAPPage />} />
        <Route path="/monitoring/audit" element={<AuditPage />} />
        <Route path="/monitoring/metrics" element={<MetricsPage />} />
        <Route path="/monitoring/logs" element={<LogsPage />} />
        <Route path="/monitoring/speedtest" element={<SpeedtestPage />} />
        <Route path="/dropzone" element={<DropzonePage />} />
        <Route path="/terminal" element={<TerminalPage />} />
        <Route path="/replication" element={<ReplicationPage />} />
        <Route path="/connect" element={<ConnectSDKPage />} />
        <Route path="/settings" element={<SettingsPage />} />
        <Route path="*" element={<Navigate to="/buckets" replace />} />
      </Routes>
    </Layout>
  );
}

export function App() {
  return (
    <ToastProvider>
      <AuthProvider>
        <Routed />
      </AuthProvider>
    </ToastProvider>
  );
}
