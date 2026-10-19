import { describe, expect, it, vi } from "vitest";
import { DataPrefetchCache, adminDataCache, invalidateAdminData } from "./dataPrefetchCache";
import { deferred } from "../test/deferred";

describe("DataPrefetchCache", () => {
  it("does not evict a replacement when an invalidated request fails later", async () => {
    const cache = new DataPrefetchCache();
    const old = deferred<string>();
    const first = cache.prefetch("plans", () => old.promise);
    const failure = expect(first).rejects.toThrow("old request failed");
    cache.invalidate("plans");
    const next = cache.prefetch("plans", () => Promise.resolve("new plans"));
    old.reject(new Error("old request failed"));
    await failure;
    expect(cache.consume("plans")).toBe(next);
    await expect(cache.consume("plans")).resolves.toBe("new plans");
  });

  it("keeps the replacement when an invalidated request succeeds later", async () => {
    const cache = new DataPrefetchCache();
    const old = deferred<string>();
    const first = cache.prefetch("plans", () => old.promise);
    cache.invalidate("plans");
    const next = cache.prefetch("plans", () => Promise.resolve("new plans"));
    old.resolve("old plans");
    await first;
    expect(cache.consume("plans")).toBe(next);
    await expect(next).resolves.toBe("new plans");
  });

  it("evicts its own failure so a subsequent request can retry", async () => {
    const cache = new DataPrefetchCache();
    await expect(cache.prefetch("plans", () => Promise.reject(new Error("unavailable")))).rejects.toThrow("unavailable");
    expect(cache.consume("plans")).toBeUndefined();
    await expect(cache.prefetch("plans", () => Promise.resolve("recovered"))).resolves.toBe("recovered");
  });

  it("reuses fresh entries and expires them at the TTL boundary", async () => {
    const cache = new DataPrefetchCache();
    const clock = vi.spyOn(Date, "now").mockReturnValue(100);
    const fetcher = vi.fn().mockResolvedValue("plans");
    const first = cache.prefetch("plans", fetcher);
    await first;
    clock.mockReturnValue(8_099);
    expect(cache.prefetch("plans", fetcher)).toBe(first);
    expect(fetcher).toHaveBeenCalledOnce();
    clock.mockReturnValue(8_100);
    expect(cache.consume("plans")).toBeUndefined();
    await cache.prefetch("plans", fetcher);
    expect(fetcher).toHaveBeenCalledTimes(2);
  });

  it("invalidates multiple adminDataCache keys via invalidateAdminData", async () => {
    adminDataCache.invalidatePrefix("");
    await adminDataCache.prefetch("users", () => Promise.resolve(["u1"]));
    await adminDataCache.prefetch("orders", () => Promise.resolve(["o1"]));
    await adminDataCache.prefetch("plans", () => Promise.resolve(["p1"]));

    invalidateAdminData("users", "orders");

    expect(adminDataCache.consume("users")).toBeUndefined();
    expect(adminDataCache.consume("orders")).toBeUndefined();
    await expect(adminDataCache.consume("plans")).resolves.toEqual(["p1"]);
    adminDataCache.invalidatePrefix("");
  });
});
