/**
 * dataPrefetchCache — lightweight in-memory prefetch cache with TTL.
 *
 * Designed for the admin panel navigation flow:
 *  • onPointerEnter → prefetch(key, fetcher)  — fires the request early
 *  • onClick        → consume(key)             — returns the in-flight / resolved promise
 *
 * The cache is intentionally module-level (singleton) so it survives component
 * remounts. A failed request is evicted immediately so the next consumer
 * triggers a fresh attempt.
 */
export class DataPrefetchCache {
  private readonly cache = new Map<string, { promise: Promise<unknown>; expiresAt: number }>();

  /**
   * Prefetch or return an existing in-flight request.
   * If a cached result exists and has not expired, it is returned immediately.
   * @param key      Stable cache key (e.g. "plans", "orders:page1")
   * @param fetcher  Function that returns the data promise
   * @param ttlMs    How long the cached promise is reusable (default: 8 s)
   */
  prefetch<T>(key: string, fetcher: () => Promise<T>, ttlMs = 8_000): Promise<T> {
    const now = Date.now();
    const entry = this.cache.get(key);
    if (entry !== undefined && entry.expiresAt > now) {
      return entry.promise as Promise<T>;
    }
    const promise = fetcher().catch((err: unknown) => {
      // Evict failed entries so the next access retries.
      this.cache.delete(key);
      throw err;
    });
    this.cache.set(key, { promise, expiresAt: now + ttlMs });
    return promise;
  }

  /**
   * Consume a cached promise without triggering a new fetch.
   * Returns `undefined` when no valid entry exists.
   */
  consume<T>(key: string): Promise<T> | undefined {
    const now = Date.now();
    const entry = this.cache.get(key);
    if (entry === undefined || entry.expiresAt <= now) return undefined;
    return entry.promise as Promise<T>;
  }

  /** Explicitly remove an entry (e.g. after a mutation that invalidates the data). */
  invalidate(key: string): void {
    this.cache.delete(key);
  }

  /** Invalidate all keys that match a given prefix. */
  invalidatePrefix(prefix: string): void {
    for (const key of this.cache.keys()) {
      if (key.startsWith(prefix)) this.cache.delete(key);
    }
  }
}

/** Singleton cache shared across the admin panel. */
export const adminDataCache = new DataPrefetchCache();
