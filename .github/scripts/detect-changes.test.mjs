import assert from "node:assert/strict";
import test from "node:test";

import { analyzeChangeSet, analyzePrScope } from "./detect-changes.mjs";

test("identifies pure frontend changes and isolates them from backend and packaging", () => {
  const changes = analyzeChangeSet(["web/src/App.tsx", "web/src/styles.css"]);
  assert.equal(changes.all, "false");
  assert.equal(changes.frontend, "true");
  assert.equal(changes.browser, "true");
  assert.equal(changes.run_web, "true");
  assert.equal(changes.run_browser_smoke, "true");
  assert.equal(changes.run_full_regression, "true");
  assert.equal(changes.packaged_browser, "false");
  assert.equal(changes.split_runtime, "false");
  assert.equal(changes.backend, "false");
  assert.equal(changes.backend_store, "false");
  assert.equal(changes.backend_xboard, "false");
  assert.equal(changes.backend_services_a, "false");
  assert.equal(changes.backend_services_b, "false");
  assert.equal(changes.backend_remainder, "false");
  assert.equal(changes.migration_drill, "false");
  assert.equal(changes.real_client_compat, "false");
  assert.equal(changes.supply_chain, "false");
});

test("identifies pure documentation and governance changes", () => {
  const changes = analyzeChangeSet(["docs/project/requirements.json", "README.md"]);
  assert.equal(changes.all, "false");
  assert.equal(changes.frontend, "false");
  assert.equal(changes.browser, "false");
  assert.equal(changes.packaged_browser, "false");
  assert.equal(changes.run_go, "false");
  assert.equal(changes.run_browser_smoke, "false");
  assert.equal(changes.run_full_regression, "false");
  assert.equal(changes.split_runtime, "false");
  assert.equal(changes.backend, "false");
  assert.equal(changes.supply_chain, "false");
});

test("keeps an ordinary frontend page on the fast path", () => {
  const changes = analyzeChangeSet(["web/src/features/notices/NoticeManagementPage.tsx", "web/src/styles.css"]);
  assert.equal(changes.run_web, "true");
  assert.equal(changes.run_browser_smoke, "false");
  assert.equal(changes.run_full_regression, "false");
});

test("identifies dedicated store changes and skips unrelated service groups", () => {
  const changes = analyzeChangeSet(["internal/store/user.go", "internal/store/store_test.go"]);
  assert.equal(changes.all, "false");
  assert.equal(changes.backend, "true");
  assert.equal(changes.backend_store, "true");
  assert.equal(changes.backend_xboard, "false");
  assert.equal(changes.backend_services_a, "false");
  assert.equal(changes.backend_services_b, "false");
  assert.equal(changes.backend_remainder, "false");
  assert.equal(changes.migration_drill, "true");
  assert.equal(changes.frontend, "false");
  assert.equal(changes.browser, "false");
  assert.equal(changes.packaged_browser, "false");
  assert.equal(changes.run_go, "true");
  assert.equal(changes.run_browser_smoke, "false");
  assert.equal(changes.run_full_regression, "false");
});

test("identifies httpapi changes and marks services-a and browser tests as affected", () => {
  const changes = analyzeChangeSet(["internal/httpapi/admin.go"]);
  assert.equal(changes.all, "false");
  assert.equal(changes.backend, "true");
  assert.equal(changes.backend_services_a, "true");
  assert.equal(changes.backend_store, "false");
  assert.equal(changes.browser, "true");
  assert.equal(changes.frontend, "false");
  assert.equal(changes.packaged_browser, "false");
  assert.equal(changes.run_browser_smoke, "true");
  assert.equal(changes.run_full_regression, "false");
});

test("identifies dedicated services-b changes (mailer, bulkops, attachments)", () => {
  const changes = analyzeChangeSet(["internal/mailer/mailer.go"]);
  assert.equal(changes.all, "false");
  assert.equal(changes.backend, "true");
  assert.equal(changes.backend_services_b, "true");
  assert.equal(changes.backend_services_a, "false");
  assert.equal(changes.backend_store, "false");
});

test("identifies root Go changes or go.mod as shared backend triggers", () => {
  const changes = analyzeChangeSet(["go.mod"]);
  assert.equal(changes.backend, "true");
  assert.equal(changes.backend_all, "true");
  assert.equal(changes.backend_store, "true");
  assert.equal(changes.backend_xboard, "true");
  assert.equal(changes.supply_chain, "true");
  assert.equal(changes.run_full_regression, "true");
});

test("triggers full regression when workflow or Dockerfile is touched", () => {
  const changes = analyzeChangeSet([".github/workflows/ci.yml"]);
  assert.equal(changes.all, "true");
  assert.equal(changes.backend, "true");
  assert.equal(changes.frontend, "true");
  assert.equal(changes.packaged_browser, "true");
  assert.equal(changes.split_runtime, "true");
  assert.equal(changes.supply_chain, "true");
  assert.equal(changes.run_go, "true");
  assert.equal(changes.run_web, "true");
  assert.equal(changes.run_browser_smoke, "true");
  assert.equal(changes.run_full_regression, "true");
});

test("defaults to full regression fail-safe when change set is empty", () => {
  const changes = analyzeChangeSet([]);
  assert.equal(changes.all, "true");
  assert.equal(changes.backend, "true");
  assert.equal(changes.frontend, "true");
  assert.equal(changes.run_go, "true");
  assert.equal(changes.run_web, "true");
  assert.equal(changes.run_full_regression, "true");
});

test("analyzePrScope assigns L0 for pure documentation and governance changes", () => {
  const scope = analyzePrScope(["docs/operations/rapid-iteration-execution.md", "AGENTS.md", "README.md"]);
  assert.equal(scope.tier, "L0");
  assert.equal(scope.ci_deploy, "false");
  assert.equal(scope.go_fast, "false");
  assert.equal(scope.go_race, "false");
  assert.equal(scope.web_fast, "false");
  assert.equal(scope.web_full, "false");
  assert.equal(scope.browser_smoke, "false");
  assert.equal(scope.supply_chain, "false");
  assert.equal(scope.migration_drill, "false");
  assert.equal(scope.real_client_compat, "false");
  assert.equal(scope.packaging_smoke, "false");
  assert.equal(scope.split_runtime, "false");
});

test("analyzePrScope assigns L1 for localized Go leaf changes without triggering go_race matrix", () => {
  const scope = analyzePrScope(["internal/mailer/mailer.go", "internal/telegrambot/bot_test.go"]);
  assert.equal(scope.tier, "L1");
  assert.equal(scope.go_fast, "true");
  assert.equal(scope.go_race, "false");
  assert.equal(scope.web_fast, "false");
  assert.equal(scope.web_full, "false");
  assert.equal(scope.browser_smoke, "false");
  assert.equal(scope.migration_drill, "false");
});

test("analyzePrScope assigns L1 for localized frontend page changes without browser E2E", () => {
  const scope = analyzePrScope(["web/src/features/notices/NoticeManagementPage.tsx", "web/src/styles.css"]);
  assert.equal(scope.tier, "L1");
  assert.equal(scope.web_fast, "true");
  assert.equal(scope.web_full, "false");
  assert.equal(scope.browser_smoke, "false");
  assert.equal(scope.go_fast, "false");
  assert.equal(scope.go_race, "false");
});

test("analyzePrScope assigns L1 for CI/deploy script edits instead of full regression", () => {
  const scope = analyzePrScope([".github/workflows/pr-gate.yml", "deploy/retain-online-only.py"]);
  assert.equal(scope.tier, "L1");
  assert.equal(scope.ci_deploy, "true");
  assert.equal(scope.go_race, "false");
  assert.equal(scope.web_full, "false");
  assert.equal(scope.packaging_smoke, "false");
});

test("analyzePrScope assigns L2 for httpapi or core frontend routing changes", () => {
  const scope = analyzePrScope(["internal/httpapi/admin.go", "web/src/App.tsx"]);
  assert.equal(scope.tier, "L2");
  assert.equal(scope.go_race, "true");
  assert.equal(scope.backend_services_a, "true");
  assert.equal(scope.backend_store, "false");
  assert.equal(scope.web_full, "true");
  assert.equal(scope.browser_smoke, "true");
  assert.equal(scope.real_client_compat, "false");
});

test("analyzePrScope assigns L3 for high-risk migration, dependency, packaging, or unknown paths", () => {
  const migScope = analyzePrScope(["internal/legacymigration/reconcile.go"]);
  assert.equal(migScope.tier, "L3");
  assert.equal(migScope.migration_drill, "true");
  assert.equal(migScope.go_fast, "true");

  const depScope = analyzePrScope(["go.mod", "go.sum"]);
  assert.equal(depScope.tier, "L3");
  assert.equal(depScope.supply_chain, "true");
  assert.equal(depScope.backend_all, "true");

  const installerComposeScope = analyzePrScope(["deploy/compose.yaml"]);
  assert.equal(installerComposeScope.tier, "L3");
  assert.equal(installerComposeScope.packaging_smoke, "true");
  assert.equal(installerComposeScope.ci_deploy, "true");

  const splitNginxScope = analyzePrScope([".docker/gateway/nginx.conf"]);
  assert.equal(splitNginxScope.tier, "L3");
  assert.equal(splitNginxScope.split_runtime, "true");

  const unknownScope = analyzePrScope(["mystery-binary.bin"]);
  assert.equal(unknownScope.tier, "L3");
  assert.equal(unknownScope.unknown_paths, "true");
  assert.equal(unknownScope.backend_all, "true");
  assert.equal(unknownScope.web_full, "true");

  const emptyScope = analyzePrScope([]);
  assert.equal(emptyScope.tier, "L3");
  assert.equal(emptyScope.unknown_paths, "true");
});

test("analyzePrScope skips go_fast when only store or httpapi _test.go files are modified", () => {
  const storeTestScope = analyzePrScope(["internal/store/sqlite_test.go"]);
  assert.equal(storeTestScope.go_race, "true");
  assert.equal(storeTestScope.go_fast, "false");
  assert.equal(storeTestScope.migration_drill, "false");
  assert.equal(storeTestScope.go_race_groups, JSON.stringify(["store"]));

  const storeImplScope = analyzePrScope(["internal/store/sqlite.go"]);
  assert.equal(storeImplScope.tier, "L3");
  assert.equal(storeImplScope.go_race, "true");
  assert.equal(storeImplScope.go_fast, "true");
  assert.equal(storeImplScope.migration_drill, "true");

  const xboardCmdScope = analyzePrScope(["cmd/xboard/main.go", "cmd/xboard/main_test.go"]);
  assert.equal(xboardCmdScope.tier, "L2");
  assert.equal(xboardCmdScope.backend_xboard, "true");
  assert.equal(xboardCmdScope.go_race, "true");
  assert.equal(xboardCmdScope.go_fast, "false");
  assert.equal(xboardCmdScope.go_race_groups, JSON.stringify(["xboard"]));
});

test("analyzePrScope classifies every tracked file in git ls-files without unknown_paths fallback", async () => {
  const { execFileSync } = await import("node:child_process");
  const tracked = execFileSync("git", ["ls-files"], { encoding: "utf8" })
    .split(/\r?\n/)
    .filter(Boolean);
  assert.ok(tracked.length > 500);
  const unknowns = tracked.filter((f) => analyzePrScope([f]).unknown_paths === "true");
  assert.deepEqual(unknowns, []);
});

test("resolveModifiedE2eSpecs separates regular E2E specs from Mailpit and external-server specs", async () => {
  const { resolveModifiedE2eSpecs, assertPlaywrightReportNotAllSkipped } = await import(
    "./detect-changes.mjs"
  );
  const classified = resolveModifiedE2eSpecs([
    "web/e2e/admin-navigation.spec.ts",
    "web/e2e/ui-state-regressions.spec.ts",
    "web/e2e/zz-orders.spec.ts",
    "web/e2e/login-links.spec.ts",
    "web/e2e/safe-access.spec.ts",
    "web/e2e/deleted-nonexistent.spec.ts"
  ]);
  assert.deepEqual(classified.regularSpecs, [
    "e2e/ui-state-regressions.spec.ts",
    "e2e/zz-orders.spec.ts"
  ]);
  assert.deepEqual(classified.packagedSpecs, [
    "e2e/login-links.spec.ts",
    "e2e/safe-access.spec.ts"
  ]);

  assert.doesNotThrow(() =>
    assertPlaywrightReportNotAllSkipped(
      JSON.stringify({ stats: { expected: 2, unexpected: 0, skipped: 0 } }),
      "e2e/zz-orders.spec.ts"
    )
  );
  assert.throws(
    () =>
      assertPlaywrightReportNotAllSkipped(
        JSON.stringify({ stats: { expected: 0, unexpected: 0, skipped: 1 } }),
        "e2e/safe-access.spec.ts"
      ),
    /executed 0 passing tests/
  );
  assert.throws(
    () =>
      assertPlaywrightReportNotAllSkipped(
        JSON.stringify({ stats: { expected: 1, unexpected: 1, skipped: 0 } }),
        "e2e/login-links.spec.ts"
      ),
    /unexpected failure/
  );
});

test("getChangedFiles and resolveGitRevisionMeta use origin/main...HEAD on non-main branch pushes to prevent cancel-in-progress tier downgrade", async () => {
  const { getChangedFiles, resolveGitRevisionMeta } = await import("./detect-changes.mjs");
  const fakeGit = (args) => {
    const cmd = args.join(" ");
    if (cmd === "rev-parse HEAD") return "deadbeef11111111111111111111111111111111\n";
    if (cmd === "merge-base origin/main HEAD") return "base000011111111111111111111111111111111\n";
    if (cmd === "diff --name-only origin/main...HEAD") {
      return "internal/store/orders.go\nweb/src/App.tsx\ndocs/README.md\n";
    }
    if (cmd === "diff --name-only prev000011111111111111111111111111111111..HEAD") {
      return "docs/README.md\n";
    }
    throw new Error(`unexpected git args: ${cmd}`);
  };

  const devEnv = {
    GITHUB_EVENT_NAME: "push",
    GITHUB_REF: "refs/heads/codex/ci-001-tiered-gates",
    GITHUB_REF_NAME: "codex/ci-001-tiered-gates",
    GITHUB_EVENT_BEFORE: "prev000011111111111111111111111111111111"
  };
  assert.deepEqual(getChangedFiles(devEnv, null, fakeGit), [
    "internal/store/orders.go",
    "web/src/App.tsx",
    "docs/README.md"
  ]);
  assert.deepEqual(resolveGitRevisionMeta(devEnv, null, fakeGit), {
    base_sha: "base000011111111111111111111111111111111",
    head_sha: "deadbeef11111111111111111111111111111111",
    merge_sha: "deadbeef11111111111111111111111111111111"
  });

  const mainEnv = {
    GITHUB_EVENT_NAME: "push",
    GITHUB_REF: "refs/heads/main",
    GITHUB_REF_NAME: "main",
    GITHUB_EVENT_BEFORE: "prev000011111111111111111111111111111111"
  };
  assert.deepEqual(getChangedFiles(mainEnv, null, fakeGit), ["docs/README.md"]);
  assert.deepEqual(resolveGitRevisionMeta(mainEnv, null, fakeGit), {
    base_sha: "prev000011111111111111111111111111111111",
    head_sha: "deadbeef11111111111111111111111111111111",
    merge_sha: "deadbeef11111111111111111111111111111111"
  });
});
