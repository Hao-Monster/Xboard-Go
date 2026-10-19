import assert from "node:assert/strict";
import test from "node:test";

import { analyzePrScope } from "./detect-changes.mjs";
import { CONDITIONAL_JOBS, evaluatePrGate } from "./verify-pr-gate.mjs";

function allSkippedResults() {
  const results = {
    "detect-changes": "success",
    "governance": "success"
  };
  for (const item of CONDITIONAL_JOBS) {
    results[item.job] = "skipped";
  }
  return results;
}

test("L0 docs scope passes when detect-changes and governance succeed and all other jobs are skipped", () => {
  const scope = analyzePrScope(["README.md", "docs/operations/rapid-iteration-execution.md"]);
  const results = allSkippedResults();
  const evaluation = evaluatePrGate(JSON.stringify(scope), results);
  assert.equal(evaluation.ok, true);
  assert.equal(evaluation.tier, "L0");
});

test("L1 Go leaf scope passes when go-fast succeeds and fails if go-fast is skipped", () => {
  const scope = analyzePrScope(["internal/mailer/mailer.go"]);
  const results = allSkippedResults();

  const failedSkip = evaluatePrGate(JSON.stringify(scope), results);
  assert.equal(failedSkip.ok, false);
  assert.ok(failedSkip.errors.some((e) => e.includes("go-fast")));

  results["go-fast"] = "success";
  const passed = evaluatePrGate(JSON.stringify(scope), results);
  assert.equal(passed.ok, true);
  assert.equal(passed.tier, "L1");
});

test("rejects cancelled or failed unscheduled jobs or invalid scope JSON", () => {
  const scope = analyzePrScope(["README.md"]);
  const results = allSkippedResults();
  results["web-fast"] = "cancelled";
  const cancelledEval = evaluatePrGate(JSON.stringify(scope), results);
  assert.equal(cancelledEval.ok, false);

  const badJsonEval = evaluatePrGate("{not-json", allSkippedResults());
  assert.equal(badJsonEval.ok, false);
});
