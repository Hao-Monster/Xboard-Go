import { execFileSync } from "node:child_process";
import { appendFileSync, readFileSync } from "node:fs";
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
  "internal/devicestate/",
  "internal/nodecoord/",
  "internal/settings/"
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
  ".github/scripts/check-split-topology",
  "internal/webui/"
];

const packagingTriggers = [
  "Dockerfile",
  ".dockerignore",
  ".docker/",
  "compose.yaml",
  "compose.local.yaml",
  "deploy/install.sh"
];

const highRiskFrontendTriggers = [
  "web/src/App.tsx",
  "web/src/main.tsx",
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

const docExactFiles = new Set([
  "AGENTS.md",
  "README.md",
  "LICENSE",
  ".gitignore",
  ".gitattributes",
  ".gitleaksignore",
  ".env.example",
  ".github/pull_request_template.md"
]);

const docPrefixes = [
  "docs/",
  "docs- box/",
  ".github/ISSUE_TEMPLATE/"
];

const ciDeployExactFiles = new Set([
  "Makefile",
  ".githooks/pre-commit",
  ".githooks/pre-push",
  ".github/dependabot.yml",
  ".github/client-compatibility.json"
]);

const ciDeployPrefixes = [
  ".github/workflows/",
  ".github/scripts/",
  "deploy/",
  "tools/",
  ".githooks/"
];

const coreWebExactFiles = new Set([
  "web/package.json",
  "web/pnpm-lock.yaml",
  "web/index.html",
  "web/src/App.tsx",
  "web/src/main.tsx"
]);

const coreWebPrefixes = [
  "web/vite.config",
  "web/tsconfig",
  "web/eslint.config",
  "web/playwright",
  "web/scripts/",
  "web/src/lib/",
  "web/src/routes/",
  "web/src/auth/",
  "web/src/features/auth/",
  "web/src/features/settings/",
  "web/e2e/"
];

function matchesPrefix(file, prefixes) {
  return prefixes.some((prefix) => file === prefix || file.startsWith(prefix));
}

export function isDocOrGovernanceFile(file) {
  if (docExactFiles.has(file)) return true;
  if (matchesPrefix(file, docPrefixes)) return true;
  if (file.endsWith(".md") && !file.startsWith("web/") && !file.startsWith("internal/") && !file.startsWith("cmd/")) {
    return true;
  }
  return false;
}

export function isCiOrDeployFile(file) {
  return ciDeployExactFiles.has(file) || matchesPrefix(file, ciDeployPrefixes);
}

export function isCoreWebFile(file) {
  return coreWebExactFiles.has(file) || matchesPrefix(file, coreWebPrefixes);
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
        f === ".github/scripts/check-real-client-compat.sh" ||
        f === ".github/client-compatibility.json"
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

/**
 * Smart risk-tiered PR gate analyzer (L0 -> L1 -> L2 -> L3).
 * Fail-closed on empty diff or unrecognized paths.
 */
export function analyzePrScope(rawFiles) {
  const files = (rawFiles ?? [])
    .map((f) => f.trim().replace(/\\/g, "/"))
    .filter((f) => f !== "");

  if (files.length === 0) {
    return {
      tier: "L3",
      reason: "empty_diff_fail_closed",
      unknown_paths: "true",
      ci_deploy: "true",
      go_fast: "false",
      go_race: "true",
      go_race_groups: JSON.stringify(["store", "xboard", "services-a", "services-b", "remainder"]),
      backend_all: "true",
      backend_store: "true",
      backend_xboard: "true",
      backend_services_a: "true",
      backend_services_b: "true",
      backend_remainder: "true",
      web_fast: "false",
      web_full: "true",
      browser_smoke: "true",
      supply_chain: "true",
      migration_drill: "true",
      real_client_compat: "true",
      packaging_smoke: "true",
      split_runtime: "true"
    };
  }

  let hasUnknown = false;
  for (const f of files) {
    const known =
      isDocOrGovernanceFile(f) ||
      isCiOrDeployFile(f) ||
      f.startsWith("cmd/") ||
      f.startsWith("internal/") ||
      f.startsWith("web/") ||
      f.endsWith(".go") ||
      matchesPrefix(f, dependencyTriggers) ||
      matchesPrefix(f, packagingTriggers) ||
      matchesPrefix(f, splitRuntimeTriggers);
    if (!known) {
      hasUnknown = true;
    }
  }

  const ciDeploy = files.some((f) => isCiOrDeployFile(f));
  const supplyChain = files.some((f) => matchesPrefix(f, dependencyTriggers));
  const packagingSmoke = files.some((f) => matchesPrefix(f, packagingTriggers));
  const splitRuntime = files.some((f) => matchesPrefix(f, splitRuntimeTriggers));

  const migrationDrill = files.some(
    (f) =>
      f.startsWith("internal/testdata/legacy/") ||
      f.startsWith("internal/legacymigration/") ||
      f.startsWith("internal/store/migrate") ||
      f.startsWith("internal/store/schema") ||
      f.startsWith("internal/store/legacy")
  );

  const realClientCompat = files.some(
    (f) =>
      f.startsWith("internal/subscription/") ||
      f.startsWith("internal/clientcatalog/") ||
      f.startsWith("internal/httpapi/subscription") ||
      f.startsWith("internal/httpapi/client") ||
      f.startsWith(".github/scripts/clientcompat/") ||
      f === ".github/scripts/check-real-client-compat.sh" ||
      f === ".github/client-compatibility.json"
  );

  const sharedBackendCore =
    hasUnknown ||
    files.some((f) => matchesPrefix(f, sharedBackendTriggers));

  const storeTouched =
    sharedBackendCore ||
    files.some((f) => matchesPrefix(f, dedicatedStore));

  const httpApiTouched =
    sharedBackendCore ||
    files.some((f) => f.startsWith("internal/httpapi/"));

  // go_fast is needed when non-shared Go packages are modified, or when non-test files in store/httpapi
  // are modified (so upstream callers of store/httpapi get compile-checked).
  const hasGoFastWork = files.some((f) => {
    const isGoRelated = f.endsWith(".go") || f.startsWith("cmd/") || f.startsWith("internal/");
    if (!isGoRelated) return false;
    if (f.startsWith("internal/testdata/legacy/drill/")) return false;
    const isHeavyDirectOnly = f.startsWith("internal/store/") || f.startsWith("internal/httpapi/");
    if (isHeavyDirectOnly && f.endsWith("_test.go")) {
      return false;
    }
    return true;
  });

  const goRace = sharedBackendCore || storeTouched || httpApiTouched;
  const goFast = hasGoFastWork && !sharedBackendCore;

  const backendAll = sharedBackendCore;
  const backendStore = sharedBackendCore || storeTouched;
  const backendXboard = sharedBackendCore;
  const backendServicesA = sharedBackendCore || httpApiTouched;
  const backendServicesB = sharedBackendCore;
  const backendRemainder = sharedBackendCore;

  const goRaceGroups = [];
  if (backendStore) goRaceGroups.push("store");
  if (backendXboard) goRaceGroups.push("xboard");
  if (backendServicesA) goRaceGroups.push("services-a");
  if (backendServicesB) goRaceGroups.push("services-b");
  if (backendRemainder) goRaceGroups.push("remainder");

  const hasAnyWeb = files.some((f) => f.startsWith("web/"));
  const hasCoreWeb = hasUnknown || files.some((f) => isCoreWebFile(f));
  const webFull = hasCoreWeb;
  const webFast = hasAnyWeb && !hasCoreWeb;

  const browserSmoke =
    hasUnknown ||
    files.some((f) => matchesPrefix(f, highRiskFrontendTriggers)) ||
    files.some((f) => f.startsWith("internal/httpapi/"));

  const hasL3Gate =
    hasUnknown ||
    sharedBackendCore ||
    supplyChain ||
    migrationDrill ||
    realClientCompat ||
    packagingSmoke ||
    splitRuntime;

  const hasL2Gate =
    goRace ||
    webFull ||
    browserSmoke;

  const hasL1Gate =
    ciDeploy ||
    goFast ||
    webFast;

  let tier = "L0";
  let reason = "docs_or_governance_only";
  if (hasL3Gate) {
    tier = "L3";
    reason = hasUnknown ? "unknown_path_conservative_escalation" : "high_risk_foundation_or_runtime_boundary";
  } else if (hasL2Gate) {
    tier = "L2";
    reason = "cross_module_core_or_route_boundary";
  } else if (hasL1Gate) {
    tier = "L1";
    reason = "localized_leaf_incremental_scope";
  }

  return {
    tier,
    reason,
    unknown_paths: String(hasUnknown),
    ci_deploy: String(ciDeploy),
    go_fast: String(goFast),
    go_race: String(goRace),
    go_race_groups: JSON.stringify(goRaceGroups),
    backend_all: String(backendAll),
    backend_store: String(backendStore),
    backend_xboard: String(backendXboard),
    backend_services_a: String(backendServicesA),
    backend_services_b: String(backendServicesB),
    backend_remainder: String(backendRemainder),
    web_fast: String(webFast),
    web_full: String(webFull),
    browser_smoke: String(browserSmoke),
    supply_chain: String(supplyChain),
    migration_drill: String(migrationDrill),
    real_client_compat: String(realClientCompat),
    packaging_smoke: String(packagingSmoke),
    split_runtime: String(splitRuntime)
  };
}

function readGithubEventPayload(env = process.env) {
  const eventPath = env.GITHUB_EVENT_PATH ?? "";
  if (!eventPath) return null;
  try {
    return JSON.parse(readFileSync(eventPath, "utf8"));
  } catch {
    return null;
  }
}

export function resolveGitRevisionMeta(env = process.env, payload = null) {
  const event = payload ?? readGithubEventPayload(env);
  let mergeSha = env.GITHUB_SHA ?? "";
  try {
    mergeSha = execFileSync("git", ["rev-parse", "HEAD"], {
      encoding: "utf8",
      stdio: ["ignore", "pipe", "ignore"]
    }).trim();
  } catch {
    // ignore
  }
  const baseSha =
    event?.pull_request?.base?.sha ??
    env.GITHUB_EVENT_BEFORE ??
    event?.before ??
    "";
  const headSha =
    event?.pull_request?.head?.sha ??
    mergeSha;
  return { base_sha: baseSha, head_sha: headSha, merge_sha: mergeSha };
}

export function getChangedFiles(env = process.env, payload = null) {
  const eventName = env.GITHUB_EVENT_NAME ?? "";
  const event = payload ?? readGithubEventPayload(env);

  if (eventName === "pull_request") {
    const baseRef = env.GITHUB_BASE_REF ?? "main";
    // When running on refs/pull/<N>/merge, HEAD^1 is the exact base commit merged into HEAD
    try {
      execFileSync("git", ["rev-parse", "--verify", "HEAD^2"], {
        encoding: "utf8",
        stdio: ["ignore", "pipe", "ignore"]
      });
      const mergedDiff = execFileSync(
        "git",
        ["diff", "--name-only", "HEAD^1..HEAD"],
        { encoding: "utf8", stdio: ["ignore", "pipe", "inherit"] }
      );
      const files = mergedDiff.split(/\r?\n/).filter((f) => f.trim() !== "");
      if (files.length > 0) return files;
    } catch {
      // Not a synthetic merge commit; fall through to origin/${baseRef}...HEAD
    }
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

  const before = env.GITHUB_EVENT_BEFORE || event?.before || "";
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
  const modeArgIdx = process.argv.indexOf("--mode");
  const mode = modeArgIdx >= 0 ? process.argv[modeArgIdx + 1] : "production";
  const revMeta = resolveGitRevisionMeta();
  const files = getChangedFiles();
  process.stdout.write(
    `Revision context: base=${revMeta.base_sha || "n/a"} head=${revMeta.head_sha || "n/a"} merge=${revMeta.merge_sha || "n/a"}\n`
  );
  process.stdout.write(`Detected changed files (${files.length}):\n`);
  for (const f of files) {
    process.stdout.write(`  - ${f}\n`);
  }

  const results = mode === "pr" ? analyzePrScope(files) : analyzeChangeSet(files);
  process.stdout.write(`Computed change scope (mode=${mode}):\n`);
  for (const [key, val] of Object.entries(results)) {
    process.stdout.write(`  ${key}: ${val}\n`);
  }

  const outputPath = process.env.GITHUB_OUTPUT;
  if (outputPath) {
    writeGithubOutputs(results, outputPath);
    if (mode === "pr") {
      const scopePayload = { ...results, ...revMeta };
      appendFileSync(outputPath, `scope_json=${JSON.stringify(scopePayload)}\n`, { encoding: "utf8" });
    }
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main();
}
