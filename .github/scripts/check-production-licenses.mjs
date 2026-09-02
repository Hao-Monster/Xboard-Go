import { readFile } from "node:fs/promises";
import { pathToFileURL } from "node:url";

const allowedLicenses = new Set([
  "Apache-2.0",
  "BSD-2-Clause",
  "BSD-3-Clause",
  "ISC",
  "MIT"
]);

export function validateLicenseReport(report) {
  if (report === null || Array.isArray(report) || typeof report !== "object") {
    throw new Error("pnpm license report must be an object keyed by SPDX license");
  }
  const licenses = Object.keys(report).sort();
  if (licenses.length === 0) throw new Error("pnpm license report is empty");

  const rejected = licenses.filter((license) => !allowedLicenses.has(license));
  if (rejected.length !== 0) {
    throw new Error(`unapproved production licenses: ${rejected.join(", ")}`);
  }
  return licenses;
}

async function main() {
  const reportPath = process.argv[2];
  if (!reportPath) throw new Error("usage: node check-production-licenses.mjs <pnpm-license-report.json>");
  const report = JSON.parse(await readFile(reportPath, "utf8"));
  const licenses = validateLicenseReport(report);
  process.stdout.write(`approved production licenses: ${licenses.join(", ")}\n`);
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main().catch((error) => {
    process.stderr.write(`${error instanceof Error ? error.message : String(error)}\n`);
    process.exitCode = 1;
  });
}
