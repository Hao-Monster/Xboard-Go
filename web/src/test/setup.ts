import "@testing-library/jest-dom/vitest";

import { afterEach } from "vitest";
import { cleanup } from "@testing-library/react";

import { swrCache } from "../lib/swr";

afterEach(() => {
  cleanup();
  swrCache.clear();
  document.getElementById("overlay-root")?.replaceChildren();
});
