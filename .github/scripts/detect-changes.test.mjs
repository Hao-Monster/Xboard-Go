import assert from "node:assert/strict";
import test from "node:test";

import { analyzeChangeSet } from "./detect-changes.mjs";

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
