import { appendFileSync } from "node:fs";
import { pathToFileURL } from "node:url";

import { analyzeChangeSet } from "./detect-changes.mjs";

export function classifyLegacyParity(rawFiles) {
  const result = analyzeChangeSet(rawFiles);
  return result.run_full_regression === "true" ? "full" : "smoke";
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const outputPath = process.env.GITHUB_OUTPUT;
  if (!outputPath) throw new Error("GITHUB_OUTPUT is required");
  const files = (process.env.CHANGED_FILES ?? "").split("\n").filter(Boolean);
  appendFileSync(outputPath, `tier=${classifyLegacyParity(files)}\n`, { encoding: "utf8" });
}
