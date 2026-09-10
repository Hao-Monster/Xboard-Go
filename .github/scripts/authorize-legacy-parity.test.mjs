import assert from "node:assert/strict";
import test from "node:test";

import { authorizeLegacyParity } from "./authorize-legacy-parity.mjs";

const repository = "Hao-Monster/Xboard-Go";
const sha = "0123456789abcdef0123456789abcdef01234567";

function workflowRun(overrides = {}) {
  return {
    EVENT_NAME: "workflow_run",
    GITHUB_REPOSITORY: repository,
    SOURCE_EVENT: "push",
    SOURCE_BRANCH: "main",
    SOURCE_REPOSITORY: repository,
    SOURCE_CONCLUSION: "success",
    SOURCE_SHA: sha,
    ...overrides
  };
}

test("authorizes only a successful main push from this repository", () => {
  assert.deepEqual(authorizeLegacyParity(workflowRun()), { authorized: "true", sha });
});

test("rejects pull request runs, including same-repository and fork pull requests", () => {
  assert.deepEqual(authorizeLegacyParity(workflowRun({ SOURCE_EVENT: "pull_request" })), {
    authorized: "false",
    sha: ""
  });
  assert.deepEqual(
    authorizeLegacyParity(workflowRun({ SOURCE_EVENT: "pull_request", SOURCE_REPOSITORY: "contributor/fork" })),
    { authorized: "false", sha: "" }
  );
});

test("rejects failed, non-main, and foreign-repository workflow runs", () => {
  for (const overrides of [
    { SOURCE_CONCLUSION: "failure" },
    { SOURCE_BRANCH: "feature" },
    { SOURCE_REPOSITORY: "contributor/fork" }
  ]) {
    assert.deepEqual(authorizeLegacyParity(workflowRun(overrides)), { authorized: "false", sha: "" });
  }
});

test("rejects every direct and unknown event", () => {
  assert.deepEqual(authorizeLegacyParity({ EVENT_NAME: "workflow_dispatch", DISPATCH_SHA: sha }), {
    authorized: "false",
    sha: ""
  });
  assert.deepEqual(authorizeLegacyParity({ EVENT_NAME: "push", SOURCE_SHA: sha }), {
    authorized: "false",
    sha: ""
  });
});

test("rejects malformed authorized commit identifiers", () => {
  assert.throws(() => authorizeLegacyParity(workflowRun({ SOURCE_SHA: "main" })), /exact lowercase commit SHA/);
});
