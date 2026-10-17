import { execFileSync } from "node:child_process";
import { dirname } from "node:path";
import { pathToFileURL } from "node:url";

import { getChangedFiles } from "./detect-changes.mjs";

export const MODULE_PREFIX = "github.com/Hao-Monster/Xboard-Go";

/**
 * Parse lines of `go list -f '{{.ImportPath}}\t{{join .Imports ","}}\t{{join .TestImports ","}}\t{{join .XTestImports ","}}' ./cmd/... ./internal/...`
 */
export function parseGoListGraph(rawOutput) {
  const packages = new Map();
  const lines = (rawOutput ?? "").split(/\r?\n/).filter((l) => l.trim() !== "");
  for (const line of lines) {
    const parts = line.split("\t");
    const importPath = (parts[0] ?? "").trim();
    if (!importPath) continue;
    const allDeps = new Set();
    for (let i = 1; i <= 3; i++) {
      const col = (parts[i] ?? "").trim();
      if (!col) continue;
      for (const dep of col.split(",")) {
        const trimmed = dep.trim();
        if (trimmed.startsWith(`${MODULE_PREFIX}/`)) {
          allDeps.add(trimmed);
        }
      }
    }
    packages.set(importPath, allDeps);
  }
  return packages;
}

/**
 * Given a map of `importPath -> Set<importedModulePackages>` and a list of changed repo-relative files,
 * compute:
 * - `directTestPackages`: packages directly modified that must run `go test -race`
 * - `compileOnlyPackages`: transitive callers of non-test modified packages that only need `go vet` + `go test -run=^$`
 */
export function resolveAffectedGoPackages(graph, rawFiles, options = {}) {
  const skipDirectSet = new Set(options.skipDirectPackages ?? []);
  const directTestSet = new Set();
  const nonTestModifiedSet = new Set();
  const unmappedGoFiles = [];

  const files = (rawFiles ?? [])
    .map((f) => f.trim().replace(/\\/g, "/"))
    .filter((f) => f !== "");

  for (const file of files) {
    if (file.startsWith("internal/testdata/legacy/drill/")) {
      continue;
    }
    const isGoCandidate =
      file.startsWith("cmd/") ||
      file.startsWith("internal/") ||
      file.startsWith("tools/") ||
      file.endsWith(".go");
    if (!isGoCandidate) {
      continue;
    }
    let dir = dirname(file).replace(/\\/g, "/");
    let matchedPkg = "";
    while (dir && dir !== ".") {
      const candidate = `${MODULE_PREFIX}/${dir}`;
      if (graph.has(candidate)) {
        matchedPkg = candidate;
        break;
      }
      const parent = dirname(dir).replace(/\\/g, "/");
      if (parent === dir) break;
      dir = parent;
    }
    if (!matchedPkg && dir === "." && graph.has(MODULE_PREFIX)) {
      matchedPkg = MODULE_PREFIX;
    }
    if (
      !matchedPkg &&
      file.startsWith("internal/testdata/legacy/") &&
      graph.has(`${MODULE_PREFIX}/internal/legacymigration`)
    ) {
      matchedPkg = `${MODULE_PREFIX}/internal/legacymigration`;
    }
    if (!matchedPkg) {
      if (file.endsWith(".go") || file.startsWith("cmd/") || file.startsWith("internal/")) {
        unmappedGoFiles.push(file);
      }
      continue;
    }
    directTestSet.add(matchedPkg);
    if (!file.endsWith("_test.go")) {
      nonTestModifiedSet.add(matchedPkg);
    }
  }

  // Build reverse dependency map: dep -> Set<importer>
  const reverseGraph = new Map();
  for (const [pkg, deps] of graph.entries()) {
    if (!reverseGraph.has(pkg)) reverseGraph.set(pkg, new Set());
    for (const dep of deps) {
      if (!reverseGraph.has(dep)) reverseGraph.set(dep, new Set());
      reverseGraph.get(dep).add(pkg);
    }
  }

  // BFS from nonTestModifiedSet to find all transitive importers
  const transitiveImporters = new Set();
  const queue = [...nonTestModifiedSet];
  while (queue.length > 0) {
    const current = queue.shift();
    const importers = reverseGraph.get(current) ?? new Set();
    for (const importer of importers) {
      if (!transitiveImporters.has(importer)) {
        transitiveImporters.add(importer);
        queue.push(importer);
      }
    }
  }

  const directTestPackages = [...directTestSet]
    .filter((pkg) => !skipDirectSet.has(pkg))
    .sort();

  const compileOnlyPackages = [...transitiveImporters]
    .filter((pkg) => !directTestSet.has(pkg))
    .sort();

  return {
    directTestPackages,
    compileOnlyPackages,
    unmappedGoFiles: unmappedGoFiles.sort()
  };
}

export function loadRepoGoGraph() {
  const output = execFileSync(
    "go",
    [
      "list",
      "-f",
      '{{.ImportPath}}\t{{join .Imports ","}}\t{{join .TestImports ","}}\t{{join .XTestImports ","}}',
      "./cmd/...",
      "./internal/...",
      "./tools/...",
      "./.github/scripts/clientcompat",
      "./internal/testdata/legacy/gen"
    ],
    { encoding: "utf8", stdio: ["ignore", "pipe", "inherit"] }
  );
  return parseGoListGraph(output);
}

export const HEAVY_RACE_GROUP_PACKAGES = [
  `${MODULE_PREFIX}/internal/store`,
  `${MODULE_PREFIX}/internal/httpapi`,
  `${MODULE_PREFIX}/cmd/xboard`,
  `${MODULE_PREFIX}/cmd/xboard-lifecycle`
];

export function needsGeoipFixture(directTestPackages) {
  return (directTestPackages ?? []).includes(`${MODULE_PREFIX}/internal/geoip`);
}

function main() {
  const runMode = process.argv.includes("--run");
  const checkGeoip = process.argv.includes("--check-geoip");
  const skipHeavy = process.argv.includes("--skip-heavy-groups");
  const skipDirectPackages = skipHeavy ? HEAVY_RACE_GROUP_PACKAGES : [];

  const graph = loadRepoGoGraph();
  const files = getChangedFiles();
  const { directTestPackages, compileOnlyPackages, unmappedGoFiles } = resolveAffectedGoPackages(
    graph,
    files,
    { skipDirectPackages }
  );

  if (checkGeoip) {
    process.stdout.write(`${needsGeoipFixture(directTestPackages) ? "true" : "false"}\n`);
    return;
  }

  process.stdout.write(
    `Resolved affected Go packages:\n` +
      `  Direct test packages (${directTestPackages.length}): ${directTestPackages.join(", ") || "none"}\n` +
      `  Compile-check dependent packages (${compileOnlyPackages.length}): ${compileOnlyPackages.join(", ") || "none"}\n` +
      `  Unmapped Go files (${unmappedGoFiles.length}): ${unmappedGoFiles.join(", ") || "none"}\n`
  );

  if (!runMode) {
    return;
  }

  if (unmappedGoFiles.length > 0) {
    process.stderr.write(
      `\n[go-fast] ERROR: refusing to pass with unmapped Go-related changes: ${unmappedGoFiles.join(", ")}\n`
    );
    process.exitCode = 1;
    return;
  }

  const allVetPackages = [...new Set([...directTestPackages, ...compileOnlyPackages])];
  if (allVetPackages.length === 0) {
    process.stderr.write(
      "\n[go-fast] ERROR: resolved zero Go packages to verify when go-fast was scheduled; refusing empty pass.\n"
    );
    process.exitCode = 1;
    return;
  }

  process.stdout.write(`\n[go-fast] Running go vet on ${allVetPackages.length} package(s)...\n`);
  execFileSync("go", ["vet", ...allVetPackages], { stdio: "inherit" });

  if (directTestPackages.length > 0) {
    process.stdout.write(`\n[go-fast] Running go test -race on ${directTestPackages.length} directly modified package(s)...\n`);
    execFileSync("go", ["test", "-race", "-timeout=10m", ...directTestPackages], {
      stdio: "inherit"
    });
  }

  if (compileOnlyPackages.length > 0) {
    process.stdout.write(`\n[go-fast] Compile-checking ${compileOnlyPackages.length} dependent package(s) and their tests (go test -run=^$)...\n`);
    execFileSync("go", ["test", "-run=^$", ...compileOnlyPackages], {
      stdio: "inherit"
    });
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main();
}
