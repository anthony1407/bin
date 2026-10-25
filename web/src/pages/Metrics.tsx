import { useEffect, useState } from "react";
import { Link } from "react-router";
import {
  Activity,
  Archive,
  ArrowRight,
  CheckCircle2,
  Database,
  HardDrive,
  RefreshCw,
  Server,
} from "lucide-react";
import { api, type Info } from "../api";
import { bytes, count, duration } from "../lib/format";
import { Button, Spinner, cx, errorText, useToast } from "../components/ui";

export function MetricsPage() {
  const [info, setInfo] = useState<Info | null>(null);
  const [loading, setLoading] = useState(true);
  const toast = useToast();

  const load = () => {
    setLoading(true);
    api
      .info()
      .then(setInfo)
      .catch((e) => toast("error", errorText(e)))
      .finally(() => setLoading(false));
  };

  useEffect(() => {
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  if (loading && !info) {
    return (
      <div className="flex justify-center py-20">
        <Spinner />
      </div>
    );
  }

  // The server leaves disk out when it cannot read it; show that, not a guess.
  const diskKnown = info?.disk?.total !== undefined && info?.disk?.free !== undefined;
  const diskTotal = info?.disk?.total ?? 0;
  const diskFree = info?.disk?.free ?? 0;
  const diskUsed = Math.max(0, diskTotal - diskFree);
  const usedPercent = diskTotal > 0 ? Math.round((diskUsed / diskTotal) * 100) : 0;

  // Circular gauge calculation
  const radius = 32;
  const circumference = 2 * Math.PI * radius;
  const strokeDashoffset = circumference - (usedPercent / 100) * circumference;

  return (
    <div className="space-y-6">
      {/* Title & Sync Action */}
      <div className="flex items-center justify-between border-b border-line pb-4">
        <h1 className="text-xl font-bold tracking-tight text-ink">Server Information</h1>
        <Button
          icon={<RefreshCw className={cx("size-4", loading && "animate-spin")} />}
          onClick={load}
        >
          Sync
        </Button>
      </div>

      {/* Main Stat Cards Grid */}
      <div className="grid grid-cols-1 gap-4 md:grid-cols-3">
        {/* Buckets Card */}
        <div className="rounded-card border border-line bg-surface p-5 shadow-sm">
          <div className="flex items-center justify-between text-muted">
            <span className="text-sm font-medium">Buckets</span>
            <Archive className="size-4" />
          </div>
          <div className="mt-3 flex items-baseline justify-between">
            <div className="text-3xl font-bold tracking-tight text-ink">
              {info ? count(info.buckets) : 0}
            </div>
            <Link
              to="/buckets"
              className="inline-flex items-center gap-1 rounded border border-line px-2 py-1 text-xs font-medium text-muted hover:bg-sunken hover:text-ink transition-colors"
            >
              Browse <ArrowRight className="size-3" />
            </Link>
          </div>
        </div>

        {/* Objects Card */}
        <div className="rounded-card border border-line bg-surface p-5 shadow-sm">
          <div className="flex items-center justify-between text-muted">
            <span className="text-sm font-medium">Objects</span>
            <Database className="size-4" />
          </div>
          <div className="mt-3 text-3xl font-bold tracking-tight text-ink">
            {info ? count(info.objects) : 0}
          </div>
        </div>

        {/* Reported Usage Card */}
        <div className="rounded-card border border-line bg-surface p-5 shadow-sm">
          <div className="flex items-center justify-between text-muted">
            <span className="text-sm font-medium">Reported Usage</span>
            <span className="text-xs text-faint">Storage</span>
          </div>
          <div className="mt-3 flex items-center justify-between">
            <div className="text-3xl font-bold tracking-tight text-ink">
              {info ? bytes(info.bytes) : "0 B"}
            </div>
            {/* Donut Gauge */}
            <div className="relative size-12 shrink-0">
              <svg className="size-full -rotate-90" viewBox="0 0 76 76">
                <circle
                  cx="38"
                  cy="38"
                  r={radius}
                  className="stroke-line"
                  strokeWidth="6"
                  fill="none"
                />
                <circle
                  cx="38"
                  cy="38"
                  r={radius}
                  className="stroke-accent transition-all duration-500"
                  strokeWidth="6"
                  strokeDasharray={circumference}
                  strokeDashoffset={strokeDashoffset}
                  strokeLinecap="round"
                  fill="none"
                />
              </svg>
              <div className="absolute inset-0 flex items-center justify-center font-mono text-[10px] font-semibold text-ink">
                {usedPercent}%
              </div>
            </div>
          </div>
        </div>
      </div>

      {/* Health & Cluster Secondary Cards */}
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
        {/* Servers status */}
        <div className="rounded-card border border-line bg-surface p-4 shadow-sm">
          <div className="flex items-center justify-between text-xs text-muted mb-2">
            <span>Servers</span>
            <Server className="size-3.5" />
          </div>
          <div className="flex items-center gap-4 text-sm font-semibold">
            <span className="flex items-center gap-1.5 text-ok">
              <span className="size-2 rounded-full bg-ok" /> 1 Online
            </span>
            <span className="flex items-center gap-1.5 text-muted">
              <span className="size-2 rounded-full bg-line-strong" /> 0 Offline
            </span>
          </div>
        </div>

        {/* Drives status */}
        <div className="rounded-card border border-line bg-surface p-4 shadow-sm">
          <div className="flex items-center justify-between text-xs text-muted mb-2">
            <span>Drives</span>
            <HardDrive className="size-3.5" />
          </div>
          <div className="flex items-center gap-4 text-sm font-semibold">
            <span className="flex items-center gap-1.5 text-ok">
              <span className="size-2 rounded-full bg-ok" /> 1 Online
            </span>
            <span className="flex items-center gap-1.5 text-muted">
              <span className="size-2 rounded-full bg-line-strong" /> 0 Offline
            </span>
          </div>
        </div>

        {/* Uptime */}
        <div className="rounded-card border border-line bg-surface p-4 shadow-sm">
          <div className="flex items-center justify-between text-xs text-muted mb-2">
            <span>Uptime</span>
            <Activity className="size-3.5" />
          </div>
          <div className="font-mono text-sm font-medium text-ink">
            {info ? duration(info.uptime) : "—"}
          </div>
        </div>

        {/* Backend Type */}
        <div className="rounded-card border border-line bg-surface p-4 shadow-sm">
          <div className="flex items-center justify-between text-xs text-muted mb-2">
            <span>Backend Type</span>
            <CheckCircle2 className="size-3.5 text-ok" />
          </div>
          <div className="text-sm font-semibold text-ink">Single Node / Drive</div>
        </div>
      </div>

      {/* Servers Table Section */}
      <div className="space-y-3">
        <h2 className="text-base font-semibold text-ink">Servers (1)</h2>
        <div className="overflow-hidden rounded-card border border-line bg-surface">
          <table className="w-full text-sm">
            <thead className="border-b border-line bg-sunken/60 text-left">
              <tr className="label-strip">
                <th className="py-2.5 pl-4 pr-3 font-semibold text-ink">Endpoint</th>
                <th className="py-2.5 pr-3 font-normal">Drives</th>
                <th className="py-2.5 pr-3 font-normal">Network</th>
                <th className="py-2.5 pr-3 font-normal">Uptime</th>
                <th className="py-2.5 pr-4 text-right font-normal">Version</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-line">
              <tr className="hover:bg-sunken/40">
                <td className="py-3 pl-4 pr-3 font-mono font-medium text-ink">
                  <div className="flex items-center gap-2">
                    <span className="size-2 rounded-full bg-ok" />
                    <span>{info?.s3Endpoint || "—"}</span>
                  </div>
                </td>
                <td className="py-3 pr-3 font-mono text-xs">
                  <span className="inline-flex items-center gap-1 font-semibold text-ok">
                    1/1 <span className="size-1.5 rounded-full bg-ok" />
                  </span>
                </td>
                <td className="py-3 pr-3 font-mono text-xs">
                  <span className="inline-flex items-center gap-1 font-semibold text-ok">
                    1/1 <span className="size-1.5 rounded-full bg-ok" />
                  </span>
                </td>
                <td className="py-3 pr-3 text-xs text-muted">
                  {info ? duration(info.uptime) : "—"}
                </td>
                <td className="py-3 pr-4 text-right font-mono text-xs text-muted">
                  {info?.version || "—"}
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>

      {/* Drives Section */}
      <div className="space-y-3">
        <h2 className="text-base font-semibold text-ink">Drives (1)</h2>
        <div className="rounded-card border border-line bg-surface p-5 shadow-sm">
          <div className="flex flex-col gap-6 sm:flex-row sm:items-center sm:justify-between">
            <div className="flex items-center gap-4">
              {/* Circular drive usage */}
              <div className="relative size-16 shrink-0">
                <svg className="size-full -rotate-90" viewBox="0 0 76 76">
                  <circle
                    cx="38"
                    cy="38"
                    r={radius}
                    className="stroke-line"
                    strokeWidth="7"
                    fill="none"
                  />
                  <circle
                    cx="38"
                    cy="38"
                    r={radius}
                    className="stroke-accent"
                    strokeWidth="7"
                    strokeDasharray={circumference}
                    strokeDashoffset={strokeDashoffset}
                    strokeLinecap="round"
                    fill="none"
                  />
                </svg>
                <div className="absolute inset-0 flex flex-col items-center justify-center">
                  <span className="font-mono text-xs font-bold text-ink">{usedPercent}%</span>
                </div>
              </div>

              <div>
                <div className="text-xs font-medium text-muted">Drive Name</div>
                <div className="font-mono text-base font-bold text-ink">
                  {info?.dataDir || "—"}
                </div>
              </div>
            </div>

            <div className="flex flex-wrap items-center gap-8 text-xs">
              <div>
                <span className="text-muted block">Used Capacity</span>
                <span className="font-mono text-sm font-semibold text-ink">{diskKnown ? bytes(diskUsed) : "—"}</span>
                <span className="text-faint block">{diskKnown ? `${usedPercent}% of ${bytes(diskTotal)}` : "Not reported by the server"}</span>
              </div>
              <div>
                <span className="text-muted block">Available Capacity</span>
                <span className="font-mono text-sm font-semibold text-ink">{diskKnown ? bytes(diskFree) : "—"}</span>
                <span className="text-faint block">{diskKnown ? `${100 - usedPercent}% free` : ""}</span>
              </div>
              <div>
                <span className="text-muted block">Drive Status</span>
                <span className="inline-flex items-center gap-1.5 font-semibold text-ok">
                  <span className="size-2 rounded-full bg-ok" /> Online Drive
                </span>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}
