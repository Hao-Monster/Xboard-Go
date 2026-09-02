import assert from "node:assert/strict";
import test from "node:test";

import { validateLicenseReport } from "./check-production-licenses.mjs";

test("accepts the reviewed production license set", () => {
  assert.deepEqual(validateLicenseReport({ MIT: [{ name: "react" }], ISC: [{ name: "trim-lines" }] }), ["ISC", "MIT"]);
});

test("rejects unknown and copyleft license groups", () => {
  assert.throws(() => validateLicenseReport({ MIT: [], UNKNOWN: [] }), /UNKNOWN/);
  assert.throws(() => validateLicenseReport({ "AGPL-3.0-only": [] }), /AGPL-3.0-only/);
});

test("rejects malformed or empty reports", () => {
  assert.throws(() => validateLicenseReport([]), /must be an object/);
  assert.throws(() => validateLicenseReport({}), /is empty/);
});
