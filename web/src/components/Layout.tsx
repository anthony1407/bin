import { useEffect, useState, type ReactNode } from "react";
import { NavLink, useLocation } from "react-router";
import {
  Activity,
  Archive,
  ArrowLeftRight,
  ChevronDown,
  ChevronRight,
  Code2,
  Contact,
  FileText,
  FolderSearch,
  Key,
  KeyRound,
  LogIn,
  LogOut,
  Menu,
  Moon,
  PanelLeftClose,
  PanelLeftOpen,
  SearchCheck,
  Settings,
  ShieldCheck,
  Sun,
  Terminal,
  UploadCloud,
  User,
  Users,
  X,
  Zap,
} from "lucide-react";
import { useAuth } from "../auth";
import { BinMark, IconButton, cx } from "./ui";

export function useTheme() {
  const [dark, setDark] = useState(() => document.documentElement.classList.contains("dark"));
  const toggle = () => {
    const next = !dark;
    document.documentElement.classList.toggle("dark", next);
    try {
      localStorage.setItem("bin-theme", next ? "dark" : "light");
    } catch {
      /* storage unavailable */
    }
    setDark(next);
  };
  return { dark, toggle };
}

interface NavItemProps {
  to: string;
  label: string;
  icon: typeof FolderSearch;
  collapsed: boolean;
  onNavigate?: () => void;
}

function NavItem({ to, label, icon: Icon, collapsed, onNavigate }: NavItemProps) {
  return (
    <NavLink
      to={to}
      onClick={onNavigate}
      title={collapsed ? label : undefined}
      className={({ isActive }) =>
        cx(
          "group flex items-center rounded-lg transition-all duration-150",
          collapsed
            ? "justify-center p-2"
            : "gap-3 px-2.5 py-1.5 text-xs font-medium",
          isActive
            ? "bg-accent/20 text-accent-ink font-semibold dark:bg-accent/30 dark:text-white"
            : "text-muted hover:bg-sunken hover:text-ink dark:text-slate-400 dark:hover:bg-slate-800/60 dark:hover:text-slate-200",
        )
      }
    >
      {({ isActive }) => (
        <>
          <div
            className={cx(
              "flex size-7 shrink-0 items-center justify-center rounded-full transition-all duration-150",
              isActive
                ? "bg-accent text-white shadow-sm ring-2 ring-accent/40"
                : "bg-sunken text-muted group-hover:bg-line-strong group-hover:text-ink dark:bg-slate-800 dark:text-slate-400 dark:group-hover:bg-slate-700 dark:group-hover:text-white",
            )}
          >
            <Icon className="size-3.5" />
          </div>
          {!collapsed && <span className="truncate">{label}</span>}
        </>
      )}
    </NavLink>
  );
}

function MinioSidebarContent({
  collapsed,
  onToggleCollapse,
  onNavigate,
}: {
  collapsed: boolean;
  onToggleCollapse: () => void;
  onNavigate?: () => void;
}) {
  const [identityOpen, setIdentityOpen] = useState<boolean>(true);
  const [monitoringOpen, setMonitoringOpen] = useState<boolean>(true);

  return (
    <div className="flex h-full flex-col justify-between">
      {/* Top Header */}
      <div>
        <div className="flex items-center justify-between border-b border-line px-2 pb-3.5 pt-1">
          {!collapsed ? (
            <div className="flex items-center gap-2.5 overflow-hidden">
              <div className="flex size-7 shrink-0 items-center justify-center rounded-lg bg-accent text-white shadow-sm">
                <BinMark className="size-4.5" />
              </div>
              <span className="text-base font-bold tracking-tight text-ink font-mono">bin</span>
            </div>
          ) : (
            <div className="mx-auto flex size-8 shrink-0 items-center justify-center rounded-lg bg-accent/20 text-accent">
              <BinMark className="size-4.5" />
            </div>
          )}

          {/* Desktop Collapse / Expand Button */}
          <button
            type="button"
            onClick={onToggleCollapse}
            title={collapsed ? "Expand sidebar" : "Collapse sidebar"}
            className="hidden size-6 items-center justify-center rounded text-muted hover:bg-sunken hover:text-ink transition-colors lg:flex"
          >
            {collapsed ? (
              <PanelLeftOpen className="size-4" />
            ) : (
              <PanelLeftClose className="size-4" />
            )}
          </button>
        </div>

        {/* Navigation list */}
        <nav className="mt-4 flex flex-col gap-5 overflow-y-auto pr-0.5">
          {/* User Section */}
          <div className="space-y-1">
            {!collapsed && (
              <div className="px-2.5 text-[10px] font-bold uppercase tracking-wider text-faint">
                User
              </div>
            )}
            <div className="space-y-0.5">
              <NavItem
                to="/buckets"
                label="Object Browser"
                icon={FolderSearch}
                collapsed={collapsed}
                onNavigate={onNavigate}
              />
              <NavItem
                to="/access-keys"
                label="Access Keys"
                icon={Contact}
                collapsed={collapsed}
                onNavigate={onNavigate}
              />
              <NavItem
                to="/dropzone"
                label="Upload Dropzones"
                icon={UploadCloud}
                collapsed={collapsed}
                onNavigate={onNavigate}
              />
              <NavItem
                to="/connect"
                label="Connect SDK"
                icon={Code2}
                collapsed={collapsed}
                onNavigate={onNavigate}
              />
            </div>
          </div>

          {/* Administrator Section */}
          <div className="space-y-1">
            {!collapsed && (
              <div className="px-2.5 text-[10px] font-bold uppercase tracking-wider text-faint">
                Administrator
              </div>
            )}
            <div className="space-y-0.5">
              <NavItem
                to="/buckets"
                label="Buckets"
                icon={Archive}
                collapsed={collapsed}
                onNavigate={onNavigate}
              />
              <NavItem
                to="/policies"
                label="Policies"
                icon={ShieldCheck}
                collapsed={collapsed}
                onNavigate={onNavigate}
              />

              {/* Identity Submenu */}
              <div>
                {!collapsed ? (
                  <button
                    type="button"
                    onClick={() => setIdentityOpen((o) => !o)}
                    className="flex h-8 w-full items-center justify-between rounded-lg px-2.5 text-xs font-medium text-muted hover:bg-sunken hover:text-ink transition-colors"
                  >
                    <span className="flex items-center gap-2.5">
                      <div className="flex size-7 shrink-0 items-center justify-center rounded-full bg-sunken text-muted dark:bg-slate-800 dark:text-slate-400">
                        <Key className="size-3.5" />
                      </div>
                      <span>Identity</span>
                    </span>
                    {identityOpen ? (
                      <ChevronDown className="size-3.5" />
                    ) : (
                      <ChevronRight className="size-3.5" />
                    )}
                  </button>
                ) : (
                  <NavLink
                    to="/identity/users"
                    title="Identity > Users"
                    className={({ isActive }) =>
                      cx(
                        "flex items-center justify-center rounded-lg p-2 transition-colors",
                        isActive ? "bg-accent/20" : "hover:bg-sunken",
                      )
                    }
                  >
                    <div className="flex size-7 items-center justify-center rounded-full bg-sunken dark:bg-slate-800">
                      <Key className="size-3.5" />
                    </div>
                  </NavLink>
                )}

                {identityOpen && !collapsed && (
                  <div className="ml-5 flex flex-col gap-0.5 border-l border-line pl-2 pt-0.5">
                    <NavLink
                      to="/identity/users"
                      onClick={onNavigate}
                      className={({ isActive }) =>
                        cx(
                          "flex h-7 items-center gap-2 rounded px-2 text-xs font-medium transition-colors",
                          isActive
                            ? "bg-accent-soft text-accent-ink font-semibold"
                            : "text-muted hover:bg-sunken hover:text-ink",
                        )
                      }
                    >
                      <User className="size-3" />
                      <span>Users</span>
                    </NavLink>
                    <div className="flex h-7 items-center gap-2 rounded px-2 text-xs text-faint cursor-not-allowed">
                      <Users className="size-3" />
                      <span>Groups</span>
                    </div>
                    <div className="flex h-7 items-center gap-2 rounded px-2 text-xs text-faint cursor-not-allowed">
                      <KeyRound className="size-3" />
                      <span>OpenID</span>
                    </div>
                    <div className="flex h-7 items-center gap-2 rounded px-2 text-xs text-faint cursor-not-allowed">
                      <LogIn className="size-3" />
                      <span>LDAP</span>
                    </div>
                  </div>
                )}
              </div>

              {/* Monitoring Submenu */}
              <div>
                {!collapsed ? (
                  <button
                    type="button"
                    onClick={() => setMonitoringOpen((o) => !o)}
                    className="flex h-8 w-full items-center justify-between rounded-lg px-2.5 text-xs font-medium text-muted hover:bg-sunken hover:text-ink transition-colors"
                  >
                    <span className="flex items-center gap-2.5">
                      <div className="flex size-7 shrink-0 items-center justify-center rounded-full bg-sunken text-muted dark:bg-slate-800 dark:text-slate-400">
                        <Activity className="size-3.5" />
                      </div>
                      <span>Monitoring</span>
                    </span>
                    {monitoringOpen ? (
                      <ChevronDown className="size-3.5" />
                    ) : (
                      <ChevronRight className="size-3.5" />
                    )}
                  </button>
                ) : (
                  <NavLink
                    to="/monitoring/metrics"
                    title="Monitoring > Metrics"
                    className={({ isActive }) =>
                      cx(
                        "flex items-center justify-center rounded-lg p-2 transition-colors",
                        isActive ? "bg-accent/20" : "hover:bg-sunken",
                      )
                    }
                  >
                    <div className="flex size-7 items-center justify-center rounded-full bg-sunken dark:bg-slate-800">
                      <Activity className="size-3.5" />
                    </div>
                  </NavLink>
                )}

                {monitoringOpen && !collapsed && (
                  <div className="ml-5 flex flex-col gap-0.5 border-l border-line pl-2 pt-0.5">
                    <NavLink
                      to="/monitoring/metrics"
                      onClick={onNavigate}
                      className={({ isActive }) =>
                        cx(
                          "flex h-7 items-center gap-2 rounded px-2 text-xs font-medium transition-colors",
                          isActive
                            ? "bg-accent-soft text-accent-ink font-semibold"
                            : "text-muted hover:bg-sunken hover:text-ink",
                        )
                      }
                    >
                      <Activity className="size-3" />
                      <span>Metrics</span>
                    </NavLink>
                    <NavLink
                      to="/monitoring/logs"
                      onClick={onNavigate}
                      className={({ isActive }) =>
                        cx(
                          "flex h-7 items-center gap-2 rounded px-2 text-xs font-medium transition-colors",
                          isActive
                            ? "bg-accent-soft text-accent-ink font-semibold"
                            : "text-muted hover:bg-sunken hover:text-ink",
                        )
                      }
                    >
                      <FileText className="size-3" />
                      <span>Logs</span>
                    </NavLink>
                    <NavLink
                      to="/monitoring/speedtest"
                      onClick={onNavigate}
                      className={({ isActive }) =>
                        cx(
                          "flex h-7 items-center gap-2 rounded px-2 text-xs font-medium transition-colors",
                          isActive
                            ? "bg-accent-soft text-accent-ink font-semibold"
                            : "text-muted hover:bg-sunken hover:text-ink",
                        )
                      }
                    >
                      <Zap className="size-3 text-accent" />
                      <span>Speedtest</span>
                    </NavLink>
                    <div className="flex h-7 items-center gap-2 rounded px-2 text-xs text-faint cursor-not-allowed">
                      <SearchCheck className="size-3" />
                      <span>Audit</span>
                    </div>
                  </div>
                )}
              </div>

              <NavItem
                to="/replication"
                label="Replication & Sync"
                icon={ArrowLeftRight}
                collapsed={collapsed}
                onNavigate={onNavigate}
              />
              <NavItem
                to="/terminal"
                label="S3 Terminal"
                icon={Terminal}
                collapsed={collapsed}
                onNavigate={onNavigate}
              />
              <NavItem
                to="/settings"
                label="Configuration"
                icon={Settings}
                collapsed={collapsed}
                onNavigate={onNavigate}
              />
            </div>
          </div>
        </nav>
      </div>

      {/* Footer */}
      <Footer collapsed={collapsed} />
    </div>
  );
}

function Footer({ collapsed }: { collapsed: boolean }) {
  const { session, signOut } = useAuth();
  const { dark, toggle } = useTheme();

  if (collapsed) {
    return (
      <div className="flex flex-col items-center gap-1.5 border-t border-line pt-3">
        <IconButton label={dark ? "Light theme" : "Dark theme"} onClick={toggle}>
          {dark ? <Sun className="size-4" /> : <Moon className="size-4" />}
        </IconButton>
        <IconButton label="Sign out" onClick={signOut}>
          <LogOut className="size-4" />
        </IconButton>
      </div>
    );
  }

  return (
    <div className="flex items-center justify-between border-t border-line pt-3 px-1">
      <div className="min-w-0">
        <div className="label-strip text-[9.5px]">signed in as</div>
        <div className="truncate font-mono text-xs text-ink font-semibold">{session?.user}</div>
      </div>
      <div className="flex items-center">
        <IconButton label={dark ? "Light theme" : "Dark theme"} onClick={toggle}>
          {dark ? <Sun className="size-4" /> : <Moon className="size-4" />}
        </IconButton>
        <IconButton label="Sign out" onClick={signOut}>
          <LogOut className="size-4" />
        </IconButton>
      </div>
    </div>
  );
}

export function Layout({ children }: { children: ReactNode }) {
  const [mobileOpen, setMobileOpen] = useState(false);
  const [collapsed, setCollapsed] = useState(() => {
    try {
      return localStorage.getItem("bin-sidebar-collapsed") === "true";
    } catch {
      return false;
    }
  });

  const toggleCollapsed = () => {
    setCollapsed((prev) => {
      const next = !prev;
      try {
        localStorage.setItem("bin-sidebar-collapsed", String(next));
      } catch {}
      return next;
    });
  };

  const location = useLocation();
  useEffect(() => setMobileOpen(false), [location.pathname]);

  return (
    <div className={cx("min-h-dvh transition-all duration-200", collapsed ? "lg:pl-16" : "lg:pl-64")}>
      {/* Desktop Sidebar (Collapsible) */}
      <aside
        className={cx(
          "fixed inset-y-0 left-0 hidden flex-col border-r border-line bg-surface p-3 transition-all duration-200 lg:flex z-30",
          collapsed ? "w-16" : "w-64",
        )}
      >
        <MinioSidebarContent
          collapsed={collapsed}
          onToggleCollapse={toggleCollapsed}
        />
      </aside>

      {/* Mobile Top bar */}
      <header className="sticky top-0 z-30 flex h-14 items-center justify-between border-b border-line bg-surface/90 px-4 backdrop-blur lg:hidden">
        <div className="flex items-center gap-2">
          <div className="flex size-7 items-center justify-center rounded-full bg-accent text-white">
            <BinMark className="size-4" />
          </div>
          <span className="font-mono font-bold text-base tracking-tight text-ink">bin</span>
        </div>
        <IconButton label="Menu" onClick={() => setMobileOpen(true)}>
          <Menu className="size-5" />
        </IconButton>
      </header>

      {/* Mobile Drawer */}
      {mobileOpen && (
        <div className="fixed inset-0 z-40 lg:hidden">
          <div className="absolute inset-0 bg-black/50 backdrop-blur-xs" onClick={() => setMobileOpen(false)} />
          <div className="absolute inset-y-0 right-0 flex w-72 flex-col bg-surface p-4 shadow-2xl">
            <div className="flex items-center justify-between pb-3 border-b border-line">
              <span className="font-mono font-bold text-base tracking-tight text-ink">bin</span>
              <IconButton label="Close menu" onClick={() => setMobileOpen(false)}>
                <X className="size-5" />
              </IconButton>
            </div>
            <div className="flex-1 overflow-y-auto pt-3">
              <MinioSidebarContent
                collapsed={false}
                onToggleCollapse={() => {}}
                onNavigate={() => setMobileOpen(false)}
              />
            </div>
          </div>
        </div>
      )}

      {/* Main Full-Width Content Container */}
      <main className="w-full min-w-0 px-4 py-6 sm:px-6 lg:px-8 lg:py-6">{children}</main>
    </div>
  );
}
