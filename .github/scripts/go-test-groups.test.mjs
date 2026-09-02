import assert from "node:assert/strict";
import test from "node:test";

import { GO_TEST_GROUPS, classifyGoPackage, partitionGoPackages } from "./go-test-groups.mjs";

const modulePath = "github.com/Hao-Monster/Xboard-Go";

test("classifies the known slow packages into balanced groups", () => {
  const cases = new Map([
    [`${modulePath}/internal/store`, "store"],
    [`${modulePath}/cmd/xboard`, "xboard"],
    [`${modulePath}/cmd/xboard-lifecycle`, "xboard"],
    [`${modulePath}/internal/httpapi`, "services-a"],
    [`${modulePath}/internal/scheduler`, "services-a"],
    [`${modulePath}/internal/backup`, "services-a"],
    [`${modulePath}/internal/attachments`, "services-b"],
    [`${modulePath}/internal/bulkops`, "services-b"],
    [`${modulePath}/internal/mailer`, "services-b"],
    [`${modulePath}/internal/clientcatalog`, "services-b"],
    [`${modulePath}/internal/legacymigration`, "services-b"],
    [`${modulePath}/internal/security`, "remainder"],
    [`${modulePath}/internal/new-package`, "remainder"]
  ]);

  for (const [packagePath, expected] of cases) {
    assert.equal(classifyGoPackage(packagePath), expected, packagePath);
  }
});

test("partitions every package exactly once and keeps new packages in the remainder", () => {
  const packages = [
    `${modulePath}/internal/store`,
    `${modulePath}/cmd/xboard`,
    `${modulePath}/internal/httpapi`,
    `${modulePath}/internal/attachments`,
    `${modulePath}/internal/new-package`
  ];

  const partitioned = partitionGoPackages(packages);
  assert.deepEqual(Object.keys(partitioned), GO_TEST_GROUPS);
  assert.deepEqual(
    Object.values(partitioned).flat().toSorted(),
    packages.toSorted()
  );
  assert.deepEqual(partitioned.remainder, [`${modulePath}/internal/new-package`]);
});

test("rejects duplicate and malformed go list output", () => {
  assert.throws(
    () => partitionGoPackages([`${modulePath}/internal/store`, `${modulePath}/internal/store`]),
    /duplicate Go package/
  );
  assert.throws(() => partitionGoPackages([""]), /empty Go package/);
});
