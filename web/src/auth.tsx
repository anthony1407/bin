import { createContext, startTransition, useCallback, useContext, useEffect, useState, type ReactNode } from "react";
import { useNavigate } from "react-router";
import { api, setUnauthorizedHandler, type Session } from "./api";

interface AuthState {
  session: Session | null;
  loading: boolean;
  /** Signs in and then shows the panel at `to`; `totp` once the server asks for a code. */
  signIn: (accessKey: string, secretKey: string, to: string, totp?: string) => Promise<void>;
  /** Signs in with directory credentials and then shows the panel at `to`. */
  signInLDAP: (username: string, password: string, to: string) => Promise<void>;
  /** Turns on a user's console access (access key + secret key once), signs in, shows `to`. */
  activate: (accessKey: string, secretKey: string, password: string, to: string, totp?: string) => Promise<void>;
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
  const finish = useCallback(
    async (login: Promise<Session>, to: string) => {
      const s = await atLeast(MIN_SIGN_IN_MS, login);
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
  const signIn = useCallback(
    (accessKey: string, secretKey: string, to: string, totp?: string) => finish(api.login(accessKey, secretKey, totp), to),
    [finish],
  );
  const signInLDAP = useCallback(
    (username: string, password: string, to: string) => finish(api.loginLDAP(username, password), to),
    [finish],
  );
  const activate = useCallback(
    (accessKey: string, secretKey: string, password: string, to: string, totp?: string) =>
      finish(api.activateConsole(accessKey, secretKey, password, totp), to),
    [finish],
  );
  const signOut = useCallback(async () => {
    await api.logout().catch(() => {});
    setSession(null);
  }, []);

  return (
    <AuthContext.Provider value={{ session, loading, signIn, signInLDAP, activate, signOut }}>{children}</AuthContext.Provider>
  );
}

export function useAuth() {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error("useAuth outside AuthProvider");
  return ctx;
}

/** Whether the signed-in session is an administrator (root, LDAP, OpenID). */
export function useIsAdmin() {
  return useAuth().session?.role !== "user";
}
