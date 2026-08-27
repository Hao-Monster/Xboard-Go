import { afterEach, describe, expect, it, vi } from "vitest";

import { secureRandomUUID } from "./random";

describe("secureRandomUUID", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("uses Web Crypto bytes when randomUUID is unavailable", () => {
    const getRandomValues = vi.fn((bytes: Uint8Array) => {
      bytes.fill(0);
      return bytes;
    });
    vi.stubGlobal("crypto", { getRandomValues });

    expect(secureRandomUUID()).toBe("00000000-0000-4000-8000-000000000000");
    expect(getRandomValues).toHaveBeenCalledOnce();
  });

  it("fails closed instead of using predictable randomness", () => {
    vi.stubGlobal("crypto", undefined);
    expect(() => secureRandomUUID()).toThrow("无法安全生成请求标识");
  });
});
