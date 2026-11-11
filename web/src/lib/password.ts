// The console password rule, the same the server enforces (auth.CheckPasswordPolicy):
// at least 6 characters with an uppercase and a lowercase letter, a number and
// a special character (anything that is neither a letter nor a digit, spaces
// included). Letters and digits of any script count.

export const MIN_PASSWORD = 6;
export const MAX_PASSWORD = 256;

export const PASSWORD_RULE =
  "At least 6 characters, with an uppercase letter, a lowercase letter, a number and a special character.";

export interface PasswordCheck {
  label: string;
  ok: boolean;
}

/** Each requirement and whether `pw` meets it, for a live checklist. */
export function passwordChecks(pw: string): PasswordCheck[] {
  const length = [...pw].length;
  return [
    { label: `At least ${MIN_PASSWORD} characters`, ok: length >= MIN_PASSWORD && length <= MAX_PASSWORD },
    { label: "An uppercase letter", ok: /\p{Lu}/u.test(pw) },
    { label: "A lowercase letter", ok: /\p{Ll}/u.test(pw) },
    { label: "A number", ok: /\p{Nd}/u.test(pw) },
    { label: "A special character", ok: /[^\p{L}\p{Nd}]/u.test(pw) },
  ];
}

/** "" when `pw` follows the rule, otherwise what it misses. */
export function passwordProblem(pw: string): string {
  const missing = passwordChecks(pw).filter((c) => !c.ok);
  return missing.length === 0 ? "" : `The password needs: ${missing.map((c) => c.label.toLowerCase()).join(", ")}.`;
}
