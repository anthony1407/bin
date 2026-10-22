import { useEffect, useState, type ReactNode } from "react";
import { NavLink, useLocation } from "react-router";
import { Archive, KeyRound, LogOut, Menu, Moon, Settings, Sun, X } from "lucide-react";
import { useAuth } from "../auth";
import { BinLogo, IconButton, cx } from "./ui";

const nav = [
  { to: "/buckets", label: "Buckets", icon: Archive },
  { to: "/access-keys", label: "Access keys", icon: KeyRound },
  { to: "/settings", label: "Settings", icon: Settings },
];

export function useTheme() {
  const [dark, setDark] = useState(() => document.documentElement.classList.contains("dark"));
  const toggle = () => {
    const next = !dark;
    document.documentElement.classList.toggle("dark", next);
    try {
      localStorage.setItem("bin-theme", next ? "dark" : "light");
    } catch {
      /* storage unavailable: the choice lasts for this page only */
    }
    setDark(next);
  };
  return { dark, toggle };
}

function Wordmark() {
  return <BinLogo className="h-7 w-auto" />;
}

function NavItems({ onNavigate }: { onNavigate?: () => void }) {
  return (
    <nav className="flex flex-col gap-0.5">
      {nav.map(({ to, label, icon: Icon }) => (
        <NavLink
          key={to}
          to={to}
          onClick={onNavigate}
          className={({ isActive }) =>
            cx(
              "flex h-9 items-center gap-3 rounded-md px-3 text-sm font-medium transition-colors",
              isActive ? "bg-accent-soft text-accent-ink" : "text-muted hover:bg-sunken hover:text-ink",
            )
          }
        >
          <Icon className="size-4" />
          {label}
        </NavLink>
      ))}
    </nav>
  );
}

function Footer() {
  const { session, signOut } = useAuth();
  const { dark, toggle } = useTheme();
  return (
    <div className="flex items-center justify-between border-t border-line pt-3">
      <div className="min-w-0">
        <div className="label-strip text-[9.5px]">signed in as</div>
        <div className="truncate font-mono text-xs text-ink">{session?.user}</div>
      </div>
      <div className="flex">
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
  const [open, setOpen] = useState(false);
  const location = useLocation();
  useEffect(() => setOpen(false), [location.pathname]);

  return (
    <div className="min-h-dvh lg:pl-60">
      {/* Desktop sidebar */}
      <aside className="fixed inset-y-0 left-0 hidden w-60 flex-col gap-6 border-r border-line bg-surface px-3 py-5 lg:flex">
        <div className="px-2">
          <Wordmark />
        </div>
        <div className="flex-1">
          <NavItems />
        </div>
        <div className="px-1">
          <Footer />
        </div>
      </aside>

      {/* Mobile top bar and drawer */}
      <header className="sticky top-0 z-30 flex h-14 items-center justify-between border-b border-line bg-surface/90 px-4 backdrop-blur lg:hidden">
        <Wordmark />
        <IconButton label="Menu" onClick={() => setOpen(true)}>
          <Menu className="size-5" />
        </IconButton>
      </header>
      {open && (
        <div className="fixed inset-0 z-40 lg:hidden">
          <div className="absolute inset-0 bg-black/40" onClick={() => setOpen(false)} />
          <div className="absolute inset-y-0 right-0 flex w-72 flex-col gap-6 bg-surface px-3 py-4 shadow-xl">
            <div className="flex items-center justify-between px-2">
              <Wordmark />
              <IconButton label="Close menu" onClick={() => setOpen(false)}>
                <X className="size-5" />
              </IconButton>
            </div>
            <div className="flex-1">
              <NavItems onNavigate={() => setOpen(false)} />
            </div>
            <div className="px-1">
              <Footer />
            </div>
          </div>
        </div>
      )}

      <main className="mx-auto w-full max-w-6xl px-4 py-6 sm:px-6 lg:px-10 lg:py-10">{children}</main>
    </div>
  );
}
