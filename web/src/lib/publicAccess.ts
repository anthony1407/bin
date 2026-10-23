// Public (anonymous read) access of one folder or object, on top of the
// bucket policy. Folders are keys ending in "/" and use prefix rules; objects
// use exact rules, so opening "a.jpg" does not open "a.jpg.bak".
import type { Access, Policy, Rule } from "../api";

export type PublicReason =
  | { kind: "private" }
  | { kind: "bucket" } // the whole bucket is public
  | { kind: "self" } // a rule for exactly this folder or object
  | { kind: "folder"; prefix: string }; // inherited from a parent folder

const reads = (a: Access) => a === "readonly" || a === "readwrite";
const isFolder = (key: string) => key.endsWith("/");

function ownRule(r: Rule, key: string) {
  return r.prefix === key && !!r.exact === !isFolder(key);
}

/** Why anonymous users can (or cannot) read key. */
export function publicReason(policy: Policy, key: string): PublicReason {
  if (policy.mode === "public") return { kind: "bucket" };
  if (policy.mode !== "custom") return { kind: "private" };
  const rules = (policy.rules ?? []).filter((r) => reads(r.access));
  if (rules.some((r) => ownRule(r, key))) return { kind: "self" };
  const parent = rules.find((r) => !r.exact && key.startsWith(r.prefix));
  return parent ? { kind: "folder", prefix: parent.prefix } : { kind: "private" };
}

/** The policy with read access for key added or removed. */
export function withPublic(policy: Policy, key: string, on: boolean): Policy {
  const rules = policy.mode === "custom" ? [...(policy.rules ?? [])] : [];
  const i = rules.findIndex((r) => ownRule(r, key));
  if (on) {
    if (i < 0) rules.push({ prefix: key, access: "readonly", ...(isFolder(key) ? {} : { exact: true }) });
    else if (rules[i].access === "writeonly") rules[i] = { ...rules[i], access: "readwrite" };
  } else if (i >= 0) {
    // Keep anonymous uploads if the rule had them.
    if (rules[i].access === "readwrite") rules[i] = { ...rules[i], access: "writeonly" };
    else rules.splice(i, 1);
  }
  return rules.length ? { mode: "custom", rules } : { mode: "private" };
}

/** The anonymous S3 URL of an object. */
export function publicURL(s3Endpoint: string, bucket: string, key: string): string {
  return `${s3Endpoint.replace(/\/+$/, "")}/${encodeURIComponent(bucket)}/${key.split("/").map(encodeURIComponent).join("/")}`;
}
