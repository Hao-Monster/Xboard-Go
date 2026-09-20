import { execFileSync } from "node:child_process";
import { appendFileSync } from "node:fs";
import { pathToFileURL } from "node:url";

const dedicatedStore = [
  "internal/store/",
  "internal/testdata/"
];

const dedicatedXboard = [
  "cmd/xboard/",
  "cmd/xboard-lifecycle/"
];

const dedicatedServicesA = [
  "internal/httpapi/",
  "internal/scheduler/",
  "internal/backup/"
];

const dedicatedServicesB = [
  "internal/attachments/",
  "internal/bulkops/",
  "internal/mailer/",
  "internal/clientcatalog/",
  "internal/legacymigration/"
];

const fullRegressionTriggers = [
  ".github/workflows/",
  ".github/scripts/detect-changes",
  "Dockerfile",
  "compose",
  "Makefile"
];

const sharedBackendTriggers = [
  "go.mod",
  "go.sum",
  "main.go",
  "embed.go",
  "internal/config/",
  "internal/security/",
  "internal/model/",
  "internal/database/",
  "internal/ratelimit/",
  "internal/devicestate/"
];

const dependencyTriggers = [
  "go.mod",
  "go.sum",
  "web/package.json",
  "web/pnpm-lock.yaml"
];

const splitRuntimeTriggers = [
  "compose.split.yaml",
  "Dockerfile.split",
  ".github/scripts/check-split-topology"
];

const packagingTriggers = [
  "Dockerfile",
  "compose.yaml",
  "compose.local.yaml"
];

const highRiskFrontendTriggers = [
  "web/src/App.tsx",
  "web/src/lib/",
  "web/src/features/auth/",
  "web/src/features/settings/",
  "web/e2e/"
];

const fullRegressionFrontendTriggers = [
  "web/src/App.tsx",
  "web/src/routes/",
  "web/src/auth/"
];

const highRiskBackendTriggers = [
  "internal/security/",
  "internal/model/",
  "internal/database/",
  "internal/ratelimit/",
  "internal/devicestate/",
  "internal/testdata/legacy/",
  "internal/legacymigration/"
];

const fullRegressionDependencyTriggers = [
  "go.mod",
  "go.sum",
  "web/package.json",
  "web/pnpm-lock.yaml"
];

function matchesPrefix(file, prefixes) {
  return prefixes.some((prefix) => file === prefix || file.startsWith(prefix));
}

function fullRegressionResult() {
  return {
    all: "true",
    backend: "true",
    backend_all: "true",
    backend_store: "true",
    backend_xboard: "true",
    backend_services_a: "true",
    backend_services_b: "true",
    backend_remainder: "true",
    migration_drill: "true",
    real_client_compat: "true",
    frontend: "true",
    browser: "true",
    packaged_browser: "true",
    split_runtime: "true",
    supply_chain: "true",
    run_go: "true",
    run_web: "true",
    run_browser_smoke: "true",
    run_full_regression: "true"
  };
}

export function analyzeChangeSet(rawFiles) {
  const files = (rawFiles ?? [])
    .map((f) => f.trim().replace(/\\/g, "/"))
    .filter((f) => f !== "");

  // If no files were detected or diff failed, fail-safe to running all tests
  if (files.length === 0) {
    return fullRegressionResult();
  }

  const isFullRegression = files.some((f) =>
    fullRegressionTriggers.some((prefix) => f === prefix || f.startsWith(prefix))
  );

  if (isFullRegression) {
    return fullRegressionResult();
  }

  const backendAll = files.some((f) =>
    sharedBackendTriggers.some((prefix) => f === prefix || f.startsWith(prefix))
  );

  const hasGoFiles = files.some(
    (f) => f.endsWith(".go") || f.startsWith("cmd/") || f.startsWith("internal/")
  );

  const backend = backendAll || hasGoFiles;

  const backendStore =
    backendAll ||
    files.some((f) => dedicatedStore.some((prefix) => f.startsWith(prefix)));

  const backendXboard =
    backendAll ||
    files.some((f) => dedicatedXboard.some((prefix) => f.startsWith(prefix)));

  const backendServicesA =
    backendAll ||
    files.some((f) => dedicatedServicesA.some((prefix) => f.startsWith(prefix)));

  const backendServicesB =
    backendAll ||
    files.some((f) => dedicatedServicesB.some((prefix) => f.startsWith(prefix)));

  const backendRemainder =
    backendAll ||
    files.some((f) => {
      if (!f.endsWith(".go") && !f.startsWith("internal/")) return false;
      const inStore = dedicatedStore.some((prefix) => f.startsWith(prefix));
      const inXboard = dedicatedXboard.some((prefix) => f.startsWith(prefix));
      const inA = dedicatedServicesA.some((prefix) => f.startsWith(prefix));
      const inB = dedicatedServicesB.some((prefix) => f.startsWith(prefix));
      return !inStore && !inXboard && !inA && !inB;
    });

  const migrationDrill =
    backendAll ||
    files.some(
      (f) =>
        f.startsWith("internal/testdata/legacy/") ||
        f.startsWith("internal/legacymigration/") ||
        f.startsWith("internal/store/")
    );

  const realClientCompat =
    backendAll ||
    files.some(
      (f) =>
        f.startsWith("internal/httpapi/") ||
        f.startsWith("internal/clientcatalog/") ||
        f.startsWith(".github/scripts/clientcompat/") ||
        f === ".github/scripts/check-real-client-compat.sh"
    );

  const frontend = files.some((f) => f.startsWith("web/"));

  const browser =
    frontend || files.some((f) => f.startsWith("internal/httpapi/"));

  const packagedBrowser = files.some((f) =>
    packagingTriggers.some((prefix) => f === prefix || f.startsWith(prefix))
  );

  const splitRuntime = files.some((f) =>
    splitRuntimeTriggers.some((prefix) => f === prefix || f.startsWith(prefix))
  );

  const supplyChain = files.some((f) =>
    dependencyTriggers.some((prefix) => f === prefix || f.startsWith(prefix))
  );

  const runGo = backend;
  const runWeb = frontend;
  const runBrowserSmoke =
    files.some((f) => matchesPrefix(f, highRiskFrontendTriggers)) ||
    files.some((f) => f.startsWith("internal/httpapi/"));
  const runFullRegression =
    files.some((f) => matchesPrefix(f, highRiskBackendTriggers)) ||
    files.some((f) => matchesPrefix(f, fullRegressionFrontendTriggers)) ||
    files.some((f) => matchesPrefix(f, fullRegressionDependencyTriggers));

  return {
    all: "false",
    backend: String(backend),
    backend_all: String(backendAll),
    backend_store: String(backendStore),
    backend_xboard: String(backendXboard),
    backend_services_a: String(backendServicesA),
    backend_services_b: String(backendServicesB),
    backend_remainder: String(backendRemainder),
    migration_drill: String(migrationDrill),
    real_client_compat: String(realClientCompat),
    frontend: String(frontend),
    browser: String(browser),
    packaged_browser: String(packagedBrowser),
    split_runtime: String(splitRuntime),
    supply_chain: String(supplyChain),
    run_go: String(runGo),
    run_web: String(runWeb),
    run_browser_smoke: String(runBrowserSmoke),
    run_full_regression: String(runFullRegression)
  };
}

export function getChangedFiles(env = process.env) {
  const eventName = env.GITHUB_EVENT_NAME ?? "";

  if (eventName === "pull_request") {
    const baseRef = env.GITHUB_BASE_REF ?? "main";
    try {
      const output = execFileSync(
        "git",
        ["diff", "--name-only", `origin/${baseRef}...HEAD`],
        { encoding: "utf8", stdio: ["ignore", "pipe", "inherit"] }
      );
      return output.split(/\r?\n/).filter((f) => f.trim() !== "");
    } catch {
      try {
        const fallback = execFileSync(
          "git",
          ["diff", "--name-only", "HEAD~1...HEAD"],
          { encoding: "utf8", stdio: ["ignore", "pipe", "inherit"] }
        );
        return fallback.split(/\r?\n/).filter((f) => f.trim() !== "");
      } catch {
        return [];
      }
    }
  }

  const before = env.GITHUB_EVENT_BEFORE ?? "";
  if (before && before !== "0000000000000000000000000000000000000000") {
    try {
      const output = execFileSync(
        "git",
        ["diff", "--name-only", `${before}..HEAD`],
        { encoding: "utf8", stdio: ["ignore", "pipe", "inherit"] }
      );
      return output.split(/\r?\n/).filter((f) => f.trim() !== "");
    } catch {
      // Fallback below
    }
  }

  try {
    const output = execFileSync("git", ["diff", "--name-only", "HEAD~1..HEAD"], {
      encoding: "utf8",
      stdio: ["ignore", "pipe", "inherit"]
    });
    return output.split(/\r?\n/).filter((f) => f.trim() !== "");
  } catch {
    return [];
  }
}

export function writeGithubOutputs(results, outputPath) {
  if (!outputPath) return;
  const lines = Object.entries(results)
    .map(([key, val]) => `${key}=${val}\n`)
    .join("");
  appendFileSync(outputPath, lines, { encoding: "utf8" });
}

function main() {
  const files = getChangedFiles();
  process.stdout.write(`Detected changed files (${files.length}):\n`);
  for (const f of files) {
    process.stdout.write(`  - ${f}\n`);
  }

  const results = analyzeChangeSet(files);
  process.stdout.write("Computed change scope:\n");
  for (const [key, val] of Object.entries(results)) {
    process.stdout.write(`  ${key}: ${val}\n`);
  }

  const outputPath = process.env.GITHUB_OUTPUT;
  if (outputPath) {
    writeGithubOutputs(results, outputPath);
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main();
}
