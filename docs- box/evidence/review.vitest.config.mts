import { fileURLToPath } from "node:url";
import base from "../../web/vite.config.ts";

// Isolated review evidence: no changes to the application's test configuration.
export default {
  ...base,
  root: fileURLToPath(new URL("../../web", import.meta.url)),
  test: {
    ...base.test,
    include: ["../docs- box/evidence/frontend-review.test.tsx"],
    coverage: { enabled: false },
  },
};
