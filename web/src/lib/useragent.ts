// A small User-Agent reader for display ("Chrome 141 · Windows"). It is a
// best effort: browsers can send anything, so the raw string stays available.

const BROWSERS: [RegExp, string][] = [
  [/Edg(?:A|iOS)?\/(\d+)/, "Edge"],
  [/OPR\/(\d+)/, "Opera"],
  [/SamsungBrowser\/(\d+)/, "Samsung Internet"],
  [/Firefox\/(\d+)/, "Firefox"],
  [/FxiOS\/(\d+)/, "Firefox"],
  [/CriOS\/(\d+)/, "Chrome"],
  [/Chrome\/(\d+)/, "Chrome"],
  [/Version\/(\d+)[\d.]* .*Safari\//, "Safari"],
  [/curl\/([\d.]+)/, "curl"],
];

const SYSTEMS: [RegExp, string][] = [
  [/Windows NT/, "Windows"],
  [/iPhone|iPad|iPod/, "iOS"],
  [/Android/, "Android"],
  [/Mac OS X|Macintosh/, "macOS"],
  [/CrOS/, "ChromeOS"],
  [/Linux/, "Linux"],
];

export function describeUserAgent(ua: string | undefined): string {
  if (!ua) return "Unknown browser";
  let browser = "";
  for (const [re, name] of BROWSERS) {
    const m = ua.match(re);
    if (m) {
      browser = `${name} ${m[1]}`;
      break;
    }
  }
  const system = SYSTEMS.find(([re]) => re.test(ua))?.[1] ?? "";
  const mobile = /Mobile|Android|iPhone/.test(ua) && system !== "iOS" && system !== "Android" ? " (mobile)" : "";
  const parts = [browser, system + mobile].filter(Boolean);
  return parts.length ? parts.join(" · ") : "Unknown browser";
}
