import { createContext, startTransition, useCallback, useContext, useEffect, useState, type ReactNode } from "react";
import { useNavigate } from "react-router";
import { api, setUnauthorizedHandler, type Session } from "./api";

interface AuthState {
  session: Session | null;
  loading: boolean;
  /** Signs in and then shows the panel at `to`. */
  signIn: (accessKey: string, secretKey: string, to: string) => Promise<void>;
  signOut: () => Promise<void>;
}

const AuthContext = createContext<AuthState | null>(null);

// A local sign-in answers in a few milliseconds; holding the progress
// indicator a little longer reads as feedback instead of a flicker.
const MIN_SIGN_IN_MS = 600;

/** Settles like `p`, but not before `ms` have passed. */
async function atLeast<T>(ms: number, p: Promise<T>): Promise<T> {
  const wait = new Promise((r) => setTimeout(r, ms));
  try {
    return await p;
  } finally {
    await wait;
  }
}

export function AuthProvider({ children }: { children: ReactNode }) {
  const [session, setSession] = useState<Session | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    setUnauthorizedHandler(() => setSession(null));
    api
      .session()
      .then(setSession)
      .catch(() => setSession(null))
      .finally(() => setLoading(false));
  }, []);

  const navigate = useNavigate();
  const signIn = useCallback(
    async (accessKey: string, secretKey: string, to: string) => {
      const s = await atLeast(MIN_SIGN_IN_MS, api.login(accessKey, secretKey));
      // The router navigates inside a transition; putting the session update
      // in the same transition renders both at once, so the panel replaces
      // the form directly, with no empty redirect frame between.
      startTransition(() => {
        setSession(s);
        navigate(to, { replace: true });
      });
    },
    [navigate],
  );
  const signOut = useCallback(async () => {
    await api.logout().catch(() => {});
    setSession(null);
  }, []);

  return <AuthContext.Provider value={{ session, loading, signIn, signOut }}>{children}</AuthContext.Provider>;
}

export function useAuth() {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error("useAuth outside AuthProvider");
  return ctx;
}
