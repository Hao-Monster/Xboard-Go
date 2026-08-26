import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      "/api": "http://127.0.0.1:8080",
      "/healthz": "http://127.0.0.1:8080",
      "/client-download": "http://127.0.0.1:8080",
      "/client-link": "http://127.0.0.1:8080",
      "/guide": "http://127.0.0.1:8080"
    }
  },
  preview: {
    host: "127.0.0.1",
    port: 4173,
    proxy: {
      "/api": "http://127.0.0.1:8080",
      "/healthz": "http://127.0.0.1:8080",
      "/client-download": "http://127.0.0.1:8080",
      "/client-link": "http://127.0.0.1:8080",
      "/guide": "http://127.0.0.1:8080"
    }
  },
  test: {
    include: ["src/**/*.test.{ts,tsx}"],
    environment: "jsdom",
    setupFiles: ["./src/test/setup.ts"],
    css: true,
    restoreMocks: true,
    fileParallelism: false,
    coverage: {
      provider: "v8",
      include: ["src/App.tsx", "src/components/*.tsx", "src/features/servers/ServerManagementPage.tsx", "src/features/admin/*.tsx", "src/features/account/*.tsx", "src/features/auth/*.tsx", "src/features/users/*.tsx", "src/features/notices/*.tsx", "src/features/clients/*.tsx", "src/features/invitations/*.tsx", "src/features/knowledge/*.tsx", "src/features/orders/*.tsx", "src/features/plans/*.tsx", "src/features/tickets/*.tsx", "src/features/system/*.tsx", "src/features/settings/*.tsx", "src/features/user/*.tsx"],
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
