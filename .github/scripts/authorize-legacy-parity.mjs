import { appendFileSync } from "node:fs";
import { pathToFileURL } from "node:url";

const exactCommitPattern = /^[0-9a-f]{40}$/;

export function authorizeLegacyParity(environment) {
  const eventName = environment.EVENT_NAME ?? "";
  let authorized = false;
  let sha = "";

  if (eventName === "workflow_run") {
    authorized =
      environment.SOURCE_EVENT === "push" &&
      environment.SOURCE_BRANCH === "main" &&
      environment.SOURCE_REPOSITORY === environment.GITHUB_REPOSITORY &&
      environment.SOURCE_CONCLUSION === "success";
    if (authorized) sha = environment.SOURCE_SHA ?? "";
  }

  if (authorized && !exactCommitPattern.test(sha)) {
    throw new Error("authorized legacy parity target must be an exact lowercase commit SHA");
  }

  return { authorized: String(authorized), sha };
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const outputPath = process.env.GITHUB_OUTPUT;
  if (!outputPath) throw new Error("GITHUB_OUTPUT is required");
  const result = authorizeLegacyParity(process.env);
  appendFileSync(outputPath, `authorized=${result.authorized}\nsha=${result.sha}\n`, { encoding: "utf8" });
}
