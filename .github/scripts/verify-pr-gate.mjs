import { appendFileSync } from "node:fs";
import { pathToFileURL } from "node:url";

const VALID_TIERS = new Set(["L0", "L1", "L2", "L3"]);

export const CONDITIONAL_JOBS = [
  { key: "ci_deploy", job: "ci-deploy-check", label: "CI & Deploy Script Verification" },
  { key: "go_fast", job: "go-fast", label: "Go Impact-Graph Incremental Tests" },
  { key: "go_race", job: "go-race", label: "Go Core Race Detector Suite" },
  { key: "web_fast", job: "web-fast", label: "Web Related Incremental Tests & Build" },
  { key: "web_full", job: "web-full", label: "Web Full Regression & Audit" },
  { key: "browser_smoke", job: "browser-smoke", label: "Playwright Browser Smoke" },
  { key: "supply_chain", job: "supply-chain", label: "Supply-Chain & License Audit" },
  { key: "migration_drill", job: "migration-drill", label: "Legacy Migration Drill" },
  { key: "real_client_compat", job: "real-client-compat", label: "Real Client Compatibility" },
  { key: "packaging_smoke", job: "packaging-smoke", label: "Docker Packaging Smoke" },
  { key: "split_runtime", job: "split-runtime", label: "Split Runtime Topology & Smoke" }
];

export function evaluatePrGate(scopeJsonRaw, jobResults) {
  const errors = [];
  const rows = [];

  if (jobResults["detect-changes"] !== "success") {
    errors.push(`detect-changes job did not succeed (got: ${jobResults["detect-changes"] ?? "missing"})`);
  }
  rows.push({
    job: "detect-changes",
    required: true,
    result: jobResults["detect-changes"] ?? "missing",
    ok: jobResults["detect-changes"] === "success"
  });

  if (jobResults["governance"] !== "success") {
    errors.push(`governance job is mandatory and did not succeed (got: ${jobResults["governance"] ?? "missing"})`);
  }
  rows.push({
    job: "governance",
    required: true,
    result: jobResults["governance"] ?? "missing",
    ok: jobResults["governance"] === "success"
  });

  let scope = null;
  try {
    scope = JSON.parse(scopeJsonRaw || "{}");
  } catch {
    errors.push("scope_json is not valid JSON");
    scope = {};
  }

  if (!VALID_TIERS.has(scope.tier)) {
    errors.push(`Invalid or missing scope tier: ${String(scope.tier)}`);
  }

  for (const item of CONDITIONAL_JOBS) {
    const flag = scope[item.key];
    if (flag !== "true" && flag !== "false") {
      errors.push(`Missing or invalid boolean flag '${item.key}' in scope_json (got: ${String(flag)})`);
    }
    const required = flag === "true";
    const actual = jobResults[item.job] ?? "missing";
    let ok = false;

    if (required) {
      ok = actual === "success";
      if (!ok) {
        errors.push(`Required job '${item.job}' (${item.label}) must be 'success', got '${actual}'`);
      }
    } else {
      ok = actual === "skipped" || actual === "success";
      if (!ok) {
        errors.push(`Unscheduled job '${item.job}' had unexpected terminal status '${actual}'`);
      }
    }

    rows.push({
      job: item.job,
      required,
      result: actual,
      ok
    });
  }

  return {
    ok: errors.length === 0,
    tier: scope.tier ?? "UNKNOWN",
    reason: scope.reason ?? "unknown",
    base_sha: scope.base_sha ?? "",
    head_sha: scope.head_sha ?? "",
    merge_sha: scope.merge_sha ?? "",
    errors,
    rows
  };
}

export function formatGateSummary(evaluation) {
  const lines = [
    `### Smart PR Gate Summary (\`${evaluation.tier}\` — \`${evaluation.reason}\`)`
  ];
  if (evaluation.base_sha || evaluation.head_sha || evaluation.merge_sha) {
    lines.push(
      "",
      `- **Base SHA**: \`${evaluation.base_sha || "n/a"}\` | **Head SHA**: \`${evaluation.head_sha || "n/a"}\` | **Merge/Test SHA**: \`${evaluation.merge_sha || "n/a"}\``
    );
  }
  lines.push(
    "",
    "| Check Job | Required by Scope | Result | Gate Status |",
    "| :--- | :---: | :---: | :---: |"
  );
  for (const r of evaluation.rows) {
    lines.push(
      `| \`${r.job}\` | ${r.required ? "Yes" : "No (Skipped)"} | \`${r.result}\` | ${r.ok ? "PASS" : "FAIL"} |`
    );
  }
  if (evaluation.errors.length > 0) {
    lines.push("", "#### Gate Failures", "");
    for (const err of evaluation.errors) {
      lines.push(`- ${err}`);
    }
  }
  return lines.join("\n") + "\n";
}

function main() {
  const scopeJsonRaw = process.env.PR_SCOPE_JSON ?? "";
  const jobResults = {
    "detect-changes": process.env.RESULT_DETECT_CHANGES ?? "",
    "governance": process.env.RESULT_GOVERNANCE ?? "",
    "ci-deploy-check": process.env.RESULT_CI_DEPLOY ?? "",
    "go-fast": process.env.RESULT_GO_FAST ?? "",
    "go-race": process.env.RESULT_GO_RACE ?? "",
    "web-fast": process.env.RESULT_WEB_FAST ?? "",
    "web-full": process.env.RESULT_WEB_FULL ?? "",
    "browser-smoke": process.env.RESULT_BROWSER_SMOKE ?? "",
    "supply-chain": process.env.RESULT_SUPPLY_CHAIN ?? "",
    "migration-drill": process.env.RESULT_MIGRATION_DRILL ?? "",
    "real-client-compat": process.env.RESULT_REAL_CLIENT_COMPAT ?? "",
    "packaging-smoke": process.env.RESULT_PACKAGING_SMOKE ?? "",
    "split-runtime": process.env.RESULT_SPLIT_RUNTIME ?? ""
  };

  const evaluation = evaluatePrGate(scopeJsonRaw, jobResults);
  const summary = formatGateSummary(evaluation);
  process.stdout.write(summary);

  if (process.env.GITHUB_STEP_SUMMARY) {
    appendFileSync(process.env.GITHUB_STEP_SUMMARY, summary, { encoding: "utf8" });
  }

  if (!evaluation.ok) {
    process.exitCode = 1;
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main();
}
