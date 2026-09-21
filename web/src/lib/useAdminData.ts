import { useCallback, useEffect, useRef, useState } from "react";

/**
 * Module-level store for the stale data cache.
 * Survives component remounts so navigating back to a page renders instantly.
 */
const staleStore = new Map<string, { data: unknown; fetchedAt: number }>();

export interface UseAdminDataOptions {
  /**
   * Time in ms after which the cached data is considered stale and a background
   * refresh is triggered. While the background fetch runs, the stale data is
   * still shown immediately (no loading flash). Defaults to 30 s.
   */
  staleTtlMs?: number;
  /** Initial prefetched promise to use instead of calling `fetcher` on mount. */
  prefetchedPromise?: Promise<unknown>;
}

export interface UseAdminDataResult<T> {
  data: T | undefined;
  /** True only on the very first load when no cached data exists yet. */
  initialLoading: boolean;
  /** True while a background refresh is running (stale data is visible). */
  refreshing: boolean;
  error: string;
  /** Call to manually force a re-fetch and clear the cache entry. */
  refresh: () => void;
}

function messageOf(cause: unknown): string {
  return cause instanceof Error ? cause.message : "加载失败";
}

/**
 * A lightweight Stale-While-Revalidate (SWR) hook for admin page data.
 *
 * Behaviour:
 *  1. If stale store has fresh data  → show it, no fetch.
 *  2. If stale store has stale data  → show it immediately, trigger background refresh.
 *  3. If no cached data              → show initialLoading=true until first fetch completes.
 *  4. If `prefetchedPromise` given   → await it (may already be resolved), skip fetch.
 *
 * Usage:
 *   const { data, initialLoading, error, refresh } = useAdminData(
 *     "plans",
 *     () => Promise.all([api.listPlans(), api.listServerGroups()]),
 *     { prefetchedPromise: adminDataCache.consume("plans") }
 *   );
 */
export function useAdminData<T>(
  key: string,
  fetcher: () => Promise<T>,
  options: UseAdminDataOptions = {},
): UseAdminDataResult<T> {
  const { staleTtlMs = 30_000, prefetchedPromise } = options;

  const cached = staleStore.get(key) as { data: T; fetchedAt: number } | undefined;
  const [data, setData] = useState<T | undefined>(cached?.data);
  const [initialLoading, setInitialLoading] = useState(cached === undefined);
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState("");
  const refreshCountRef = useRef(0);
  // Keep fetcher stable reference to avoid spurious effect runs.
  const fetcherRef = useRef(fetcher);
  fetcherRef.current = fetcher;

  const doFetch = useCallback((isBackground: boolean, overridePromise?: Promise<T>) => {
    const seq = ++refreshCountRef.current;
    if (isBackground) setRefreshing(true);
    else setInitialLoading(true);
    setError("");

    const promise = overridePromise ?? fetcherRef.current();
    void promise.then((result) => {
      if (seq !== refreshCountRef.current) return;
      staleStore.set(key, { data: result, fetchedAt: Date.now() });
      setData(result);
      setError("");
    }).catch((cause: unknown) => {
      if (seq !== refreshCountRef.current) return;
      setError(messageOf(cause));
    }).finally(() => {
      if (seq !== refreshCountRef.current) return;
      setInitialLoading(false);
      setRefreshing(false);
    });
  }, [key]);

  useEffect(() => {
    const now = Date.now();
    const isStale = cached === undefined || now - cached.fetchedAt > staleTtlMs;

    if (!isStale) {
      // Data is fresh — nothing to do. Update state in case key changed.
      setData(cached.data);
      setInitialLoading(false);
      return;
    }

    if (cached !== undefined) {
      // Stale data available — show it immediately, refresh in background.
      setData(cached.data);
      setInitialLoading(false);
      doFetch(true, prefetchedPromise as Promise<T> | undefined);
    } else {
      // No cached data — use prefetch promise if available, otherwise fetch.
      doFetch(false, prefetchedPromise as Promise<T> | undefined);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key, staleTtlMs, doFetch]);

  const refresh = useCallback(() => {
    staleStore.delete(key);
    doFetch(false);
  }, [key, doFetch]);

  return { data, initialLoading, refreshing, error, refresh };
}

/** Imperatively invalidate a cache key (e.g. after a create/update/delete mutation). */
export function invalidateAdminData(key: string): void {
  staleStore.delete(key);
}

/** Invalidate all keys with a given prefix. */
export function invalidateAdminDataPrefix(prefix: string): void {
  for (const k of staleStore.keys()) {
    if (k.startsWith(prefix)) staleStore.delete(k);
  }
}
