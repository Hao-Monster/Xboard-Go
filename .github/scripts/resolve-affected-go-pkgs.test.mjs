import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import test from "node:test";

import {
  HEAVY_RACE_GROUP_PACKAGES,
  MODULE_PREFIX,
  needsGeoipFixture,
  parseGoListGraph,
  resolveAffectedGoPackages
} from "./resolve-affected-go-pkgs.mjs";

const sampleGraphOutput = [
  `${MODULE_PREFIX}/internal/mailtemplate\tfmt,strings\ttesting\t`,
  `${MODULE_PREFIX}/internal/mailer\t${MODULE_PREFIX}/internal/mailtemplate,${MODULE_PREFIX}/internal/store\ttesting\t`,
  `${MODULE_PREFIX}/internal/httpapi\t${MODULE_PREFIX}/internal/mailer,${MODULE_PREFIX}/internal/store\ttesting\t`,
  `${MODULE_PREFIX}/cmd/xboard\t${MODULE_PREFIX}/internal/httpapi\t\t`,
  `${MODULE_PREFIX}/internal/theme\tarchive/zip\ttesting\t`
].join("\n");

test("test-only Go file modification only tests that package and skips transitive dependents", () => {
  const graph = parseGoListGraph(sampleGraphOutput);
  const res = resolveAffectedGoPackages(graph, ["internal/mailer/mailer_test.go"]);
  assert.deepEqual(res.directTestPackages, [`${MODULE_PREFIX}/internal/mailer`]);
  assert.deepEqual(res.compileOnlyPackages, []);
});

test("non-test leaf Go file modification tests leaf package and compile-checks transitive callers", () => {
  const graph = parseGoListGraph(sampleGraphOutput);
  const res = resolveAffectedGoPackages(graph, ["internal/mailtemplate/template.go"]);
  assert.deepEqual(res.directTestPackages, [`${MODULE_PREFIX}/internal/mailtemplate`]);
  assert.deepEqual(res.compileOnlyPackages, [
    `${MODULE_PREFIX}/cmd/xboard`,
    `${MODULE_PREFIX}/internal/httpapi`,
    `${MODULE_PREFIX}/internal/mailer`
  ]);
});

test("skipDirectPackages excludes store and httpapi when handled by dedicated go-race job", () => {
  const graph = parseGoListGraph(sampleGraphOutput);
  const res = resolveAffectedGoPackages(
    graph,
    ["internal/mailer/mailer.go", "internal/httpapi/admin.go"],
    { skipDirectPackages: HEAVY_RACE_GROUP_PACKAGES }
  );
  assert.deepEqual(res.directTestPackages, [`${MODULE_PREFIX}/internal/mailer`]);
  assert.deepEqual(res.compileOnlyPackages, [`${MODULE_PREFIX}/cmd/xboard`]);
});

test("embedded subdirectory assets walk up to their owning Go package", () => {
  const graph = parseGoListGraph(sampleGraphOutput);
  const res = resolveAffectedGoPackages(graph, [
    "internal/mailtemplate/assets/welcome.html",
    "internal/httpapi/testdata/legacy_v1_admission.golden.yaml"
  ]);
  assert.deepEqual(res.directTestPackages, [
    `${MODULE_PREFIX}/internal/httpapi`,
    `${MODULE_PREFIX}/internal/mailtemplate`
  ]);
  assert.deepEqual(res.compileOnlyPackages, [
    `${MODULE_PREFIX}/cmd/xboard`,
    `${MODULE_PREFIX}/internal/mailer`
  ]);
  assert.deepEqual(res.unmappedGoFiles, []);
});

test("resolves tools Go helper packages and reports unmapped Go files for fail-closed handling", () => {
  const graph = parseGoListGraph(
    `${sampleGraphOutput}\n${MODULE_PREFIX}/tools/local-parity\tfmt,os\t\t`
  );
  const mapped = resolveAffectedGoPackages(graph, ["tools/local-parity/clean-web-root.go"]);
  assert.deepEqual(mapped.directTestPackages, [`${MODULE_PREFIX}/tools/local-parity`]);
  assert.deepEqual(mapped.unmappedGoFiles, []);

  const unmapped = resolveAffectedGoPackages(graph, ["tools/unknown/orphan.go"]);
  assert.deepEqual(unmapped.directTestPackages, []);
  assert.deepEqual(unmapped.unmappedGoFiles, ["tools/unknown/orphan.go"]);
});

test("needsGeoipFixture detects internal/geoip in directTestPackages and CLI --check-geoip executes cleanly", () => {
  const graph = parseGoListGraph(
    `${sampleGraphOutput}\n${MODULE_PREFIX}/internal/geoip\tos,strings\ttesting\t`
  );
  const withGeoip = resolveAffectedGoPackages(graph, ["internal/geoip/legacy.go"], {
    skipDirectPackages: HEAVY_RACE_GROUP_PACKAGES
  });
  assert.equal(needsGeoipFixture(withGeoip.directTestPackages), true);

  const withoutGeoip = resolveAffectedGoPackages(graph, ["internal/mailer/mailer.go"], {
    skipDirectPackages: HEAVY_RACE_GROUP_PACKAGES
  });
  assert.equal(needsGeoipFixture(withoutGeoip.directTestPackages), false);

  const cliOut = execFileSync(
    process.execPath,
    [".github/scripts/resolve-affected-go-pkgs.mjs", "--skip-heavy-groups", "--check-geoip"],
    { encoding: "utf8" }
  ).trim();
  assert.ok(cliOut === "true" || cliOut === "false");
});
