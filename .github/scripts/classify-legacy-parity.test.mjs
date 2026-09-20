import assert from "node:assert/strict";
import test from "node:test";

import { classifyLegacyParity } from "./classify-legacy-parity.mjs";

test("documentation-only changes use the lightweight parity path", () => {
  assert.equal(classifyLegacyParity(["docs/project/STATUS.md"]), "smoke");
});

test("workflow changes retain the strict parity path", () => {
  assert.equal(classifyLegacyParity([".github/workflows/ci.yml"]), "full");
});

test("high-risk application changes retain the strict parity path", () => {
  assert.equal(classifyLegacyParity(["internal/security/session.go"]), "full");
  assert.equal(classifyLegacyParity(["web/src/routes/admin.tsx"]), "full");
});

test("a low-risk isolated UI change uses the lightweight parity path", () => {
  assert.equal(classifyLegacyParity(["web/src/features/theme/ThemePanel.tsx"]), "smoke");
});
