import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      "/api": "http://127.0.0.1:8080",
      "/healthz": "http://127.0.0.1:8080"
    }
  },
  preview: {
    host: "127.0.0.1",
    port: 4173,
    proxy: {
      "/api": "http://127.0.0.1:8080",
      "/healthz": "http://127.0.0.1:8080"
    }
  },
  test: {
    include: ["src/**/*.test.{ts,tsx}"],
    environment: "jsdom",
    setupFiles: ["./src/test/setup.ts"],
    css: true,
    restoreMocks: true,
    coverage: {
      provider: "v8",
      include: ["src/components/Overlay.tsx", "src/features/servers/ServerManagementPage.tsx", "src/features/admin/*.tsx"],
      reporter: ["text", "json-summary"],
      thresholds: {
        statements: 70,
        branches: 55,
        functions: 70,
        lines: 70
      }
    }
  }
});
