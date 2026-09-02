import { execFileSync } from "node:child_process";
import { pathToFileURL } from "node:url";

export const GO_TEST_GROUPS = ["store", "xboard", "services-a", "services-b", "remainder"];

const dedicatedPackages = new Map([
  ["/internal/store", "store"],
  ["/cmd/xboard", "xboard"],
  ["/cmd/xboard-lifecycle", "xboard"],
  ["/internal/httpapi", "services-a"],
  ["/internal/scheduler", "services-a"],
  ["/internal/backup", "services-a"],
  ["/internal/attachments", "services-b"],
  ["/internal/bulkops", "services-b"],
  ["/internal/mailer", "services-b"],
  ["/internal/clientcatalog", "services-b"],
  ["/internal/legacymigration", "services-b"]
]);

export function classifyGoPackage(packagePath) {
  for (const [suffix, group] of dedicatedPackages) {
    if (packagePath.endsWith(suffix)) return group;
  }
  return "remainder";
}

export function partitionGoPackages(packages) {
  const result = Object.fromEntries(GO_TEST_GROUPS.map((group) => [group, []]));
  const seen = new Set();

  for (const rawPackage of packages) {
    const packagePath = rawPackage.trim();
    if (!packagePath) throw new Error("empty Go package in go list output");
    if (seen.has(packagePath)) throw new Error(`duplicate Go package: ${packagePath}`);
    seen.add(packagePath);
    result[classifyGoPackage(packagePath)].push(packagePath);
  }

  return result;
}

export function listGoPackages() {
  const output = execFileSync("go", ["list", "./..."], {
    encoding: "utf8",
    stdio: ["ignore", "pipe", "inherit"]
  });
  return output.split(/\r?\n/u).filter((line) => line.trim() !== "");
}

function main() {
  const requestedGroup = process.argv[2];
  if (!GO_TEST_GROUPS.includes(requestedGroup)) {
    process.stderr.write(`usage: node .github/scripts/go-test-groups.mjs <${GO_TEST_GROUPS.join("|")}>\n`);
    process.exitCode = 2;
    return;
  }

  const packages = partitionGoPackages(listGoPackages())[requestedGroup];
  if (packages.length === 0) throw new Error(`Go test group is empty: ${requestedGroup}`);
  process.stdout.write(`${packages.join("\n")}\n`);
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) main();
