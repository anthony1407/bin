import { spawn } from "node:child_process";
import { defineConfig, type Plugin } from "vite";
import react from "@vitejs/plugin-react";

// Runs the Tailwind CLI in watch mode next to the dev server, so
// `npm run dev` is the only command needed while working on the panel.
function tailwindWatch(): Plugin {
  return {
    name: "tailwind-cli-watch",
    apply: "serve",
    configureServer(server) {
      const child = spawn("npx", ["tailwindcss", "-i", "src/styles.css", "-o", "src/generated/tailwind.css", "--watch=always"], {
        stdio: "inherit",
        shell: process.platform === "win32",
      });
      server.httpServer?.on("close", () => child.kill());
      process.on("exit", () => child.kill());
    },
  };
}

// The build goes straight into the Go package that embeds it.
// In development, `npm run dev` proxies the API to a running bin console
// (BIN_CONSOLE_URL, default http://localhost:9001). The proxy must keep the
// browser's Host header (changeOrigin: false; the string shorthand would
// rewrite it), or the console's same-origin check refuses the login.
export default defineConfig({
  plugins: [react(), tailwindWatch()],
  build: {
    outDir: "../internal/webui/dist",
    emptyOutDir: true,
    assetsDir: "assets",
    sourcemap: false,
  },
  server: {
    proxy: {
      "/api": { target: process.env.BIN_CONSOLE_URL ?? "http://localhost:9001", changeOrigin: false },
    },
  },
});
