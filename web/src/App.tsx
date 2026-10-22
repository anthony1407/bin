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

function Routed() {
  const { session, loading } = useAuth();
  const location = useLocation();
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
