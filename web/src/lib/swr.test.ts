import { describe, expect, it, vi } from "vitest";
import { SWRCacheManager } from "./swr";

describe("SWRCacheManager", () => {
  it("stores and retrieves cached values with staleness check", () => {
    const cache = new SWRCacheManager();
    expect(cache.get("test")).toBeNull();
    expect(cache.peek("test")).toBeNull();

    cache.set("test", { hello: "world" }, 1000);
    const hit = cache.get<{ hello: string }>("test");
    expect(hit).not.toBeNull();
    expect(hit?.data).toEqual({ hello: "world" });
    expect(hit?.isStale).toBe(false);
    expect(cache.peek<{ hello: string }>("test")).toEqual({ hello: "world" });
  });

  it("deduplicates concurrent in-flight fetches", async () => {
    const cache = new SWRCacheManager();
    let calls = 0;
    const fetcher = vi.fn(async () => {
      calls++;
      await new Promise((r) => setTimeout(r, 10));
      return `result-${calls}`;
    });

    const [p1, p2] = await Promise.all([
      cache.fetch("key", fetcher),
      cache.fetch("key", fetcher)
    ]);

    expect(p1).toBe("result-1");
    expect(p2).toBe("result-1");
    expect(fetcher).toHaveBeenCalledTimes(1);
  });

  it("invalidates cache by pattern or completely", () => {
    const cache = new SWRCacheManager();
    cache.set("admin:users", [1, 2]);
    cache.set("admin:orders", [3, 4]);
    cache.set("user:profile", { name: "test" });

    cache.invalidate("admin:");
    expect(cache.get("admin:users")).toBeNull();
    expect(cache.get("admin:orders")).toBeNull();
    expect(cache.get("user:profile")).not.toBeNull();

    cache.clear();
    expect(cache.get("user:profile")).toBeNull();
  });

  it("notifies activity listeners during fetch operations", async () => {
    const cache = new SWRCacheManager();
    const activityLog: number[] = [];
    const unsubscribe = cache.subscribeActivity((count) => {
      activityLog.push(count);
    });

    await cache.fetch("key", async () => {
      await Promise.resolve();
      return "done";
    });

    unsubscribe();
    expect(activityLog).toEqual([0, 1, 0]);
  });
});
