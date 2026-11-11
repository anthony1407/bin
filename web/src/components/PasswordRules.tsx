import { Check, X } from "lucide-react";
import { passwordChecks } from "../lib/password";
import { cx } from "./ui";

/** The password rule as a checklist that ticks off as the user types. */
export function PasswordRules({ password }: { password: string }) {
  return (
    <ul className="grid grid-cols-1 gap-x-4 gap-y-0.5 pt-1 text-[11px] sm:grid-cols-2" aria-label="Password requirements">
      {passwordChecks(password).map((c) => (
        <li key={c.label} className={cx("flex items-center gap-1.5", c.ok ? "text-ok" : "text-muted")}>
          {c.ok ? <Check className="size-3" /> : <X className="size-3" />}
          {c.label}
        </li>
      ))}
    </ul>
  );
}
