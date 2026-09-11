export interface SWREntry<T> {
  data: T;
  timestamp: number;
  ttl: number;
}

export interface SWROptions<T> {
  ttl?: number;
  onSuccess?: (data: T) => void;
  onError?: (error: unknown) => void;
  forceRefresh?: boolean;
}

export class SWRCacheManager {
  private cache = new Map<string, SWREntry<unknown>>();
  private inflight = new Map<string, Promise<unknown>>();
  private listeners = new Set<(activeCount: number) => void>();
  private activeFetches = 0;

  get<T>(key: string): { data: T; isStale: boolean } | null {
    const entry = this.cache.get(key) as SWREntry<T> | undefined;
    if (!entry) return null;
    const isStale = Date.now() - entry.timestamp > entry.ttl;
    return { data: entry.data, isStale };
  }

  peek<T>(key: string): T | null {
    return (this.cache.get(key) as SWREntry<T> | undefined)?.data ?? null;
  }

  findPrefix<T>(prefix: string): T | null {
    for (const [key, entry] of this.cache.entries()) {
      if (key.includes(prefix)) {
        return entry.data as T;
      }
    }
    return null;
  }

  set<T>(key: string, data: T, ttl = 180_000): void {
    this.cache.set(key, { data, timestamp: Date.now(), ttl });
  }

  invalidate(pattern?: string | RegExp): void {
    if (!pattern) {
      this.cache.clear();
      return;
    }
    for (const key of this.cache.keys()) {
      if (typeof pattern === "string" ? key.startsWith(pattern) : pattern.test(key)) {
        this.cache.delete(key);
      }
    }
  }

  clear(): void {
    this.cache.clear();
    this.inflight.clear();
    this.notify(0);
  }

  subscribeActivity(listener: (activeCount: number) => void): () => void {
    this.listeners.add(listener);
    listener(this.activeFetches);
    return () => {
      this.listeners.delete(listener);
    };
  }

  private notify(count: number): void {
    this.activeFetches = count;
    for (const listener of this.listeners) {
      try {
        listener(count);
      } catch {
        // preserve listener execution
      }
    }
  }

  async fetch<T>(key: string, fetcher: () => Promise<T>, options?: SWROptions<T>): Promise<T> {
    const ttl = options?.ttl ?? 180_000;

    const existing = this.inflight.get(key) as Promise<T> | undefined;
    if (existing && !options?.forceRefresh) {
      return existing;
    }

    this.notify(this.activeFetches + 1);
    const promise = (async () => {
      try {
        const data = await fetcher();
        this.set(key, data, ttl);
        options?.onSuccess?.(data);
        return data;
      } catch (err) {
        options?.onError?.(err);
        throw err;
      } finally {
        this.inflight.delete(key);
        this.notify(Math.max(0, this.activeFetches - 1));
      }
    })();

    this.inflight.set(key, promise);
    return promise;
  }
}

export const swrCache = new SWRCacheManager();
