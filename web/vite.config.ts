import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// The build goes straight into the Go package that embeds it.
// In development, `npm run dev` proxies the API to a running bin console.
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: "../internal/webui/dist",
    emptyOutDir: true,
    assetsDir: "assets",
    sourcemap: false,
  },
  server: {
    proxy: {
      "/api": process.env.BIN_CONSOLE_URL ?? "http://localhost:9001",
    },
  },
});
