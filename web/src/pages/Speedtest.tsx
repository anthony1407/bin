import { useState, useRef, useEffect } from "react";
import {
  ArrowDown,
  ArrowUp,
  Clock,
  History,
  Play,
  Square,
} from "lucide-react";
import { api } from "../api";
import { Button, Card, PageHeader, cx, useToast } from "../components/ui";

interface TestResult {
  id: string;
  timestamp: string;
  sizeMB: number;
  pingMs: number;
  downloadMBps: number;
  uploadMBps: number;
}

export function SpeedtestPage() {
  const [running, setRunning] = useState(false);
  const [stage, setStage] = useState<"idle" | "ping" | "download" | "upload" | "done">("idle");
  const [testSizeMB, setTestSizeMB] = useState<number>(15); // 15 MiB standard
  const [currentPing, setCurrentPing] = useState<number | null>(null);
  const [currentDownloadSpeed, setCurrentDownloadSpeed] = useState<number | null>(null); // MB/s
  const [currentUploadSpeed, setCurrentUploadSpeed] = useState<number | null>(null); // MB/s
  const [progressPct, setProgressPct] = useState<number>(0);
  const [history, setHistory] = useState<TestResult[]>(() => {
    try {
      const saved = localStorage.getItem("bin_speedtest_history");
      return saved ? JSON.parse(saved) : [];
    } catch {
      return [];
    }
  });

  const abortControllerRef = useRef<AbortController | null>(null);
  const toast = useToast();

  useEffect(() => {
    try {
      localStorage.setItem("bin_speedtest_history", JSON.stringify(history.slice(0, 10)));
    } catch {
      // ignore
    }
  }, [history]);

  function stopTest() {
    if (abortControllerRef.current) {
      abortControllerRef.current.abort();
      abortControllerRef.current = null;
    }
    setRunning(false);
    setStage("idle");
    setProgressPct(0);
  }

  async function startTest() {
    setRunning(true);
    setProgressPct(0);
    setCurrentPing(null);
    setCurrentDownloadSpeed(null);
    setCurrentUploadSpeed(null);

    const abortController = new AbortController();
    abortControllerRef.current = abortController;

    try {
      // --- Phase 1: Latency / Ping ---
      setStage("ping");
      setProgressPct(10);
      const pingSamples: number[] = [];
      for (let i = 0; i < 4; i++) {
        if (abortController.signal.aborted) return;
        const t0 = performance.now();
        await api.speedtestPing();
        const diff = performance.now() - t0;
        pingSamples.push(diff);
      }
      const avgPing = Math.round(pingSamples.reduce((a, b) => a + b, 0) / pingSamples.length);
      setCurrentPing(avgPing);
      setProgressPct(25);

      // --- Phase 2: Download Speed ---
      setStage("download");
      const sizeBytes = testSizeMB * 1024 * 1024;
      const downloadUrl = api.speedtestDownloadURL(sizeBytes);

      const downloadStart = performance.now();
      let receivedBytes = 0;

      const resp = await fetch(downloadUrl, {
        signal: abortController.signal,
        cache: "no-store",
      });

      if (!resp.ok) throw new Error("Download speedtest failed");

      const reader = resp.body?.getReader();
      if (!reader) throw new Error("ReadableStream not supported");

      while (true) {
        if (abortController.signal.aborted) return;
        const { done, value } = await reader.read();
        if (done) break;
        receivedBytes += value.length;
        const elapsedSec = (performance.now() - downloadStart) / 1000;
        if (elapsedSec > 0.05) {
          const mbps = receivedBytes / (1024 * 1024) / elapsedSec;
          setCurrentDownloadSpeed(Math.round(mbps * 10) / 10);
        }
        const pct = 25 + Math.min(35, (receivedBytes / sizeBytes) * 35);
        setProgressPct(Math.round(pct));
      }

      const totalDownloadTime = (performance.now() - downloadStart) / 1000;
      const finalDownloadSpeed =
        Math.round((receivedBytes / (1024 * 1024) / totalDownloadTime) * 10) / 10;
      setCurrentDownloadSpeed(finalDownloadSpeed);
      setProgressPct(60);

      // --- Phase 3: Upload Speed ---
      setStage("upload");
      // Prepare random buffer
      const uploadBytes = testSizeMB * 1024 * 1024;
      const randomBuf = new Uint8Array(uploadBytes);
      for (let i = 0; i < uploadBytes; i += 1024) {
        randomBuf[i] = (i * 31) % 256;
      }
      const uploadBlob = new Blob([randomBuf], { type: "application/octet-stream" });

      const uploadStart = performance.now();
      const res = await api.speedtestUpload(
        uploadBlob,
        (sent) => {
          const elapsed = (performance.now() - uploadStart) / 1000;
          if (elapsed > 0.05) {
            const mbps = sent / (1024 * 1024) / elapsed;
            setCurrentUploadSpeed(Math.round(mbps * 10) / 10);
          }
          const pct = 60 + Math.min(38, (sent / uploadBytes) * 38);
          setProgressPct(Math.round(pct));
        },
        abortController.signal
      );

      const finalUploadSpeed = Math.round(res.mBps * 10) / 10;
      setCurrentUploadSpeed(finalUploadSpeed);
      setProgressPct(100);
      setStage("done");

      // Save history
      const result: TestResult = {
        id: String(Date.now()),
        timestamp: new Date().toLocaleTimeString(),
        sizeMB: testSizeMB,
        pingMs: avgPing,
        downloadMBps: finalDownloadSpeed,
        uploadMBps: finalUploadSpeed,
      };
      setHistory((prev) => [result, ...prev]);
      toast("ok", `Benchmark completed: ${finalDownloadSpeed} MB/s down, ${finalUploadSpeed} MB/s up!`);
    } catch (err: any) {
      if (err.name !== "AbortError" && !abortController.signal.aborted) {
        toast("error", err.message || "Benchmark aborted");
      }
    } finally {
      setRunning(false);
    }
  }

  return (
    <div className="space-y-6 max-w-5xl pb-16">
      <PageHeader
        title="Network & Storage Speedtest"
        subtitle="Benchmark real-time transfer bandwidth, latency, and disk write throughput directly with your bin S3 server."
      />

      {/* Main Speedtest Box */}
      <Card className="overflow-hidden p-6 relative">
        {/* Top Control Bar */}
        <div className="flex flex-col sm:flex-row sm:items-center justify-between pb-6 border-b border-line gap-4">
          <div className="flex items-center gap-3">
            <span className="label-strip">Payload Size:</span>
            <div className="flex rounded-lg border border-line bg-sunken p-0.5 text-xs">
              {[
                { size: 5, label: "5 MB (Fast)" },
                { size: 15, label: "15 MB (Normal)" },
                { size: 40, label: "40 MB (Heavy)" },
              ].map((opt) => (
                <button
                  key={opt.size}
                  disabled={running}
                  onClick={() => setTestSizeMB(opt.size)}
                  className={cx(
                    "px-3 py-1 rounded-md font-medium transition-all",
                    testSizeMB === opt.size
                      ? "bg-accent text-white shadow-sm font-semibold"
                      : "text-muted hover:text-ink disabled:opacity-50"
                  )}
                >
                  {opt.label}
                </button>
              ))}
            </div>
          </div>

          <div className="flex items-center gap-3">
            {!running ? (
              <Button
                variant="primary"
                icon={<Play className="size-4 fill-current" />}
                onClick={startTest}
                className="px-6"
              >
                Start Benchmark
              </Button>
            ) : (
              <Button
                variant="danger"
                icon={<Square className="size-4 fill-current" />}
                onClick={stopTest}
              >
                Stop Test
              </Button>
            )}
          </div>
        </div>

        {/* Live Gauges & Stat Cards */}
        <div className="grid grid-cols-1 md:grid-cols-3 gap-6 py-8">
          {/* Latency */}
          <div className="flex flex-col items-center justify-center p-6 rounded-xl border border-line bg-sunken/40">
            <div className="flex items-center gap-2 text-muted mb-2">
              <Clock className="size-4 text-accent" />
              <span className="text-xs font-semibold uppercase tracking-wider">Latency / Ping</span>
            </div>
            <div className="flex items-baseline gap-1 my-2">
              <span className="font-mono text-4xl font-bold tracking-tight text-ink">
                {currentPing !== null ? currentPing : "--"}
              </span>
              <span className="text-sm font-mono text-muted">ms</span>
            </div>
            <span className="text-xs text-muted">
              {currentPing !== null
                ? currentPing < 5
                  ? "⚡ Ultra low latency (LAN/Local)"
                  : currentPing < 30
                  ? "✨ Low latency"
                  : "🌐 Standard network"
                : stage === "ping"
                ? "Measuring TTFB..."
                : "Idle"}
            </span>
          </div>

          {/* Download Speed */}
          <div className={cx(
            "flex flex-col items-center justify-center p-6 rounded-xl border transition-all",
            stage === "download"
              ? "border-accent bg-accent/5 ring-2 ring-accent/20"
              : "border-line bg-sunken/40"
          )}>
            <div className="flex items-center gap-2 text-muted mb-2">
              <ArrowDown className="size-4 text-ok" />
              <span className="text-xs font-semibold uppercase tracking-wider">Download Speed</span>
            </div>
            <div className="flex items-baseline gap-1 my-2">
              <span className="font-mono text-4xl font-bold tracking-tight text-ink">
                {currentDownloadSpeed !== null ? currentDownloadSpeed : "--"}
              </span>
              <span className="text-sm font-mono text-muted">MB/s</span>
            </div>
            <span className="text-xs text-muted">
              {currentDownloadSpeed !== null
                ? `≈ ${Math.round(currentDownloadSpeed * 8)} Mbps`
                : stage === "download"
                ? "Streaming chunks..."
                : "Idle"}
            </span>
          </div>

          {/* Upload Speed */}
          <div className={cx(
            "flex flex-col items-center justify-center p-6 rounded-xl border transition-all",
            stage === "upload"
              ? "border-accent bg-accent/5 ring-2 ring-accent/20"
              : "border-line bg-sunken/40"
          )}>
            <div className="flex items-center gap-2 text-muted mb-2">
              <ArrowUp className="size-4 text-accent" />
              <span className="text-xs font-semibold uppercase tracking-wider">Upload Speed</span>
            </div>
            <div className="flex items-baseline gap-1 my-2">
              <span className="font-mono text-4xl font-bold tracking-tight text-ink">
                {currentUploadSpeed !== null ? currentUploadSpeed : "--"}
              </span>
              <span className="text-sm font-mono text-muted">MB/s</span>
            </div>
            <span className="text-xs text-muted">
              {currentUploadSpeed !== null
                ? `≈ ${Math.round(currentUploadSpeed * 8)} Mbps`
                : stage === "upload"
                ? "Writing to server..."
                : "Idle"}
            </span>
          </div>
        </div>

        {/* Live Progress Bar */}
        <div className="space-y-2 pt-2">
          <div className="flex justify-between text-xs text-muted">
            <span className="capitalize font-medium">
              {running ? `Running: ${stage} phase...` : stage === "done" ? "Test completed" : "Ready"}
            </span>
            <span className="font-mono">{progressPct}%</span>
          </div>
          <div className="h-2.5 w-full overflow-hidden rounded-full bg-sunken">
            <div
              className="h-full bg-accent transition-all duration-300 ease-out"
              style={{ width: `${progressPct}%` }}
            />
          </div>
        </div>
      </Card>

      {/* History of Past Runs */}
      {history.length > 0 && (
        <Card className="overflow-hidden">
          <div className="flex items-center justify-between border-b border-line px-5 py-3.5 bg-sunken/40">
            <div className="flex items-center gap-2 font-medium text-sm">
              <History className="size-4 text-muted" />
              <span>Recent Benchmark Runs</span>
            </div>
            <button
              onClick={() => setHistory([])}
              className="text-xs text-muted hover:text-danger transition-colors"
            >
              Clear History
            </button>
          </div>
          <div className="divide-y divide-line">
            {history.map((item) => (
              <div
                key={item.id}
                className="grid grid-cols-2 sm:grid-cols-5 items-center px-5 py-3 text-xs gap-3 hover:bg-sunken/20 transition-colors"
              >
                <div className="font-mono text-muted">{item.timestamp}</div>
                <div className="text-muted">Payload: {item.sizeMB} MB</div>
                <div>
                  <span className="text-muted mr-1.5">Ping:</span>
                  <span className="font-mono font-medium">{item.pingMs} ms</span>
                </div>
                <div>
                  <span className="text-muted mr-1.5">Down:</span>
                  <span className="font-mono font-medium text-ok">{item.downloadMBps} MB/s</span>
                </div>
                <div>
                  <span className="text-muted mr-1.5">Up:</span>
                  <span className="font-mono font-medium text-accent">{item.uploadMBps} MB/s</span>
                </div>
              </div>
            ))}
          </div>
        </Card>
      )}
    </div>
  );
}
