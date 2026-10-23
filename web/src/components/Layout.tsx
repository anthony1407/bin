import { useEffect, useState, type ReactNode } from "react";
import { NavLink, useLocation, useNavigate } from "react-router";
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
  Search,
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
import { api, type Bucket } from "../api";
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

/** An entry of an expanded submenu (Identity, Monitoring). */
function SubNavItem({ to, label, icon: Icon, onNavigate }: Omit<NavItemProps, "collapsed">) {
  return (
    <NavLink
      to={to}
      onClick={onNavigate}
      className={({ isActive }) =>
        cx(
          "flex h-7 items-center gap-2 rounded px-2 text-xs font-medium transition-colors",
          isActive ? "bg-accent-soft text-accent-ink font-semibold" : "text-muted hover:bg-sunken hover:text-ink",
        )
      }
    >
      <Icon className="size-3" />
      <span>{label}</span>
    </NavLink>
  );
}

interface PaletteItem {
  label: string;
  section: string;
  icon: typeof FolderSearch;
  to?: string;
  badge?: string;
  action?: () => void;
}

function CommandPalette({
  open,
  onClose,
}: {
  open: boolean;
  onClose: () => void;
}) {
  const [query, setQuery] = useState("");
  const [buckets, setBuckets] = useState<Bucket[]>([]);
  const [selectedIndex, setSelectedIndex] = useState(0);
  const navigate = useNavigate();
  const { toggle: toggleTheme } = useTheme();

  useEffect(() => {
    if (open) {
      setQuery("");
      setSelectedIndex(0);
      api.buckets().then(setBuckets).catch(() => {});
    }
  }, [open]);

  const navItems: PaletteItem[] = [
    { label: "Object Browser", to: "/buckets", icon: FolderSearch, section: "Navigation" },
    { label: "Access Keys", to: "/access-keys", icon: Contact, section: "Navigation" },
    { label: "Upload Dropzones", to: "/dropzone", icon: UploadCloud, section: "Navigation" },
    { label: "Connect SDK", to: "/connect", icon: Code2, section: "Navigation" },
    { label: "Buckets", to: "/buckets", icon: Archive, section: "Administrator" },
    { label: "Policies", to: "/policies", icon: ShieldCheck, section: "Administrator" },
    { label: "Users", to: "/identity/users", icon: User, section: "Administrator" },
    { label: "Groups", to: "/identity/groups", icon: Users, section: "Administrator" },
    { label: "Metrics", to: "/monitoring/metrics", icon: Activity, section: "Monitoring" },
    { label: "Logs", to: "/monitoring/logs", icon: FileText, section: "Monitoring" },
    { label: "Speedtest", to: "/monitoring/speedtest", icon: Zap, section: "Monitoring" },
    { label: "Replication & Sync", to: "/replication", icon: ArrowLeftRight, section: "Administrator" },
    { label: "S3 Terminal", to: "/terminal", icon: Terminal, section: "Administrator" },
    { label: "Configuration", to: "/settings", icon: Settings, section: "Administrator" },
  ];

  const bucketItems: PaletteItem[] = buckets.map((b) => ({
    label: b.name,
    to: `/buckets/${encodeURIComponent(b.name)}`,
    icon: Archive,
    section: "Buckets",
    badge: `${b.objects} objs`,
  }));

  const actionItems: PaletteItem[] = [
    {
      label: "Create New User",
      to: "/identity/users",
      icon: User,
      section: "Actions",
    },
    {
      label: "New Access Key",
      to: "/access-keys",
      icon: Key,
      section: "Actions",
    },
    {
      label: "Toggle Theme (Dark / Light)",
      action: () => toggleTheme(),
      icon: Sun,
      section: "Actions",
    },
  ];

  const allItems: PaletteItem[] = [...bucketItems, ...navItems, ...actionItems];

  const filtered = query.trim()
    ? allItems.filter(
        (item) =>
          item.label.toLowerCase().includes(query.toLowerCase()) ||
          item.section.toLowerCase().includes(query.toLowerCase()),
      )
    : allItems.slice(0, 15);

  const executeItem = (item: PaletteItem) => {
    onClose();
    if (item.action) {
      item.action();
    } else if (item.to) {
      navigate(item.to);
    }
  };

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "ArrowDown") {
      e.preventDefault();
      setSelectedIndex((i) => (i + 1) % Math.max(1, filtered.length));
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      setSelectedIndex((i) => (i - 1 + filtered.length) % Math.max(1, filtered.length));
    } else if (e.key === "Enter" && filtered[selectedIndex]) {
      e.preventDefault();
      executeItem(filtered[selectedIndex]);
    } else if (e.key === "Escape") {
      onClose();
    }
  };

  if (!open) return null;

  return (
    <div className="fixed inset-0 z-50 flex items-start justify-center pt-20 p-4">
      <div className="fixed inset-0 bg-black/50 backdrop-blur-xs" onClick={onClose} />
      <div className="relative w-full max-w-lg overflow-hidden rounded-xl border border-line bg-surface shadow-2xl transition-all">
        {/* Search Input Bar */}
        <div className="flex items-center gap-3 border-b border-line px-4 py-3">
          <Search className="size-5 text-faint shrink-0" />
          <input
            autoFocus
            value={query}
            onChange={(e) => {
              setQuery(e.target.value);
              setSelectedIndex(0);
            }}
            onKeyDown={handleKeyDown}
            placeholder="Type a command, page, or bucket name…"
            className="w-full bg-transparent text-sm text-ink placeholder:text-muted focus:outline-none font-medium"
          />
          <kbd className="rounded border border-line bg-sunken px-1.5 py-0.5 font-mono text-[10px] text-faint">
            ESC
          </kbd>
        </div>

        {/* Results list */}
        <div className="max-h-80 overflow-y-auto p-2 space-y-1">
          {filtered.length === 0 ? (
            <div className="py-8 text-center text-xs text-muted">No results found for “{query}”.</div>
          ) : (
            filtered.map((item, index) => {
              const Icon = item.icon;
              const isSelected = index === selectedIndex;
              return (
                <div
                  key={`${item.section}-${item.label}-${index}`}
                  onClick={() => executeItem(item)}
                  onMouseEnter={() => setSelectedIndex(index)}
                  className={cx(
                    "flex items-center justify-between rounded-lg px-3 py-2 text-xs cursor-pointer transition-colors",
                    isSelected
                      ? "bg-accent/15 text-accent-ink font-semibold dark:bg-accent/25 dark:text-white"
                      : "text-ink hover:bg-sunken",
                  )}
                >
                  <div className="flex items-center gap-2.5 min-w-0">
                    <div
                      className={cx(
                        "flex size-6 items-center justify-center rounded-md",
                        isSelected ? "bg-accent text-white" : "bg-sunken text-muted",
                      )}
                    >
                      <Icon className="size-3.5" />
                    </div>
                    <span className="truncate">{item.label}</span>
                  </div>
                  <div className="flex items-center gap-2 shrink-0">
                    {item.badge && (
                      <span className="rounded bg-sunken px-1.5 py-0.5 text-[10px] font-mono text-muted">
                        {item.badge}
                      </span>
                    )}
                    <span className="text-[10px] uppercase font-bold text-faint tracking-wider">
                      {item.section}
                    </span>
                  </div>
                </div>
              );
            })
          )}
        </div>

        {/* Footer shortcuts */}
        <div className="flex items-center justify-between border-t border-line bg-sunken/40 px-3 py-2 text-[11px] text-faint">
          <div className="flex items-center gap-3">
            <span><kbd className="rounded border border-line px-1 text-[10px]">↑</kbd> <kbd className="rounded border border-line px-1 text-[10px]">↓</kbd> navigate</span>
            <span><kbd className="rounded border border-line px-1 text-[10px]">↵</kbd> select</span>
          </div>
          <span>Global Search (Ctrl+K)</span>
        </div>
      </div>
    </div>
  );
}

function SidebarContent({
  collapsed,
  onToggleCollapse,
  onNavigate,
  onOpenSearch,
}: {
  collapsed: boolean;
  onToggleCollapse: () => void;
  onNavigate?: () => void;
  onOpenSearch?: () => void;
}) {
  const [identityOpen, setIdentityOpen] = useState<boolean>(true);
  const [monitoringOpen, setMonitoringOpen] = useState<boolean>(true);

  return (
    <div className="flex h-full min-h-0 flex-col">
      {/* Header and footer stay put; only the navigation list scrolls between them. */}
      <div className="flex min-h-0 flex-1 flex-col">
        <div className="flex shrink-0 items-center justify-between border-b border-line px-2 pb-3.5 pt-1">
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

        {/* Quick Search Button (Ctrl+K) */}
        <div className="pt-2.5 pb-1 px-1">
          {!collapsed ? (
            <button
              type="button"
              onClick={onOpenSearch}
              className="flex w-full items-center justify-between rounded-lg border border-line bg-sunken/60 px-2.5 py-1.5 text-xs text-muted hover:border-accent/40 hover:text-ink hover:bg-sunken transition-all shadow-xs"
            >
              <span className="flex items-center gap-2">
                <Search className="size-3.5 text-faint" />
                <span>Quick search…</span>
              </span>
              <kbd className="rounded border border-line bg-surface px-1.5 py-0.5 font-mono text-[10px] text-faint shadow-2xs">
                Ctrl K
              </kbd>
            </button>
          ) : (
            <button
              type="button"
              onClick={onOpenSearch}
              title="Quick Search (Ctrl+K)"
              className="mx-auto flex size-8 items-center justify-center rounded-lg text-muted hover:bg-sunken hover:text-ink transition-colors"
            >
              <Search className="size-4" />
            </button>
          )}
        </div>

        {/* Navigation list */}
        <nav className="flex min-h-0 flex-1 flex-col gap-5 overflow-x-hidden overflow-y-auto pt-4 pb-3 pr-0.5">
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
                    <SubNavItem to="/identity/users" label="Users" icon={User} onNavigate={onNavigate} />
                    <SubNavItem to="/identity/groups" label="Groups" icon={Users} onNavigate={onNavigate} />
                    <SubNavItem to="/identity/openid" label="OpenID" icon={KeyRound} onNavigate={onNavigate} />
                    <SubNavItem to="/identity/ldap" label="LDAP" icon={LogIn} onNavigate={onNavigate} />
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
                    <SubNavItem to="/monitoring/metrics" label="Metrics" icon={Activity} onNavigate={onNavigate} />
                    <SubNavItem to="/monitoring/logs" label="Logs" icon={FileText} onNavigate={onNavigate} />
                    <SubNavItem to="/monitoring/speedtest" label="Speedtest" icon={Zap} onNavigate={onNavigate} />
                    <SubNavItem to="/monitoring/audit" label="Audit" icon={SearchCheck} onNavigate={onNavigate} />
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
      <div className="flex shrink-0 flex-col items-center gap-1.5 border-t border-line pt-3">
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
    <div className="flex shrink-0 items-center justify-between border-t border-line pt-3 px-1">
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
  const [paletteOpen, setPaletteOpen] = useState(false);
  const [collapsed, setCollapsed] = useState(() => {
    try {
      return localStorage.getItem("bin-sidebar-collapsed") === "true";
    } catch {
      return false;
    }
  });

  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setPaletteOpen((prev) => !prev);
      }
    };
    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, []);

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
      {/* Command Palette */}
      <CommandPalette open={paletteOpen} onClose={() => setPaletteOpen(false)} />

      {/* Desktop Sidebar (Collapsible) */}
      <aside
        className={cx(
          "fixed inset-y-0 left-0 hidden flex-col border-r border-line bg-surface p-3 transition-all duration-200 lg:flex z-30",
          collapsed ? "w-16" : "w-64",
        )}
      >
        <SidebarContent
          collapsed={collapsed}
          onToggleCollapse={toggleCollapsed}
          onOpenSearch={() => setPaletteOpen(true)}
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
        <div className="flex items-center gap-1">
          <IconButton label="Search (Ctrl+K)" onClick={() => setPaletteOpen(true)}>
            <Search className="size-5" />
          </IconButton>
          <IconButton label="Menu" onClick={() => setMobileOpen(true)}>
            <Menu className="size-5" />
          </IconButton>
        </div>
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
            <div className="min-h-0 flex-1 pt-3">
              <SidebarContent
                collapsed={false}
                onToggleCollapse={() => {}}
                onNavigate={() => setMobileOpen(false)}
                onOpenSearch={() => {
                  setMobileOpen(false);
                  setPaletteOpen(true);
                }}
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
