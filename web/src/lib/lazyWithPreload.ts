import { lazy, type LazyExoticComponent } from "react";

type ReactLazyLoader = Parameters<typeof lazy>[0];
type ReactLazyComponent = Awaited<ReturnType<ReactLazyLoader>>["default"];

type PreloadableLazyComponent<Component extends ReactLazyComponent> = LazyExoticComponent<Component> & {
  preload: () => Promise<{ default: Component }>;
};

export function lazyWithPreload<Component extends ReactLazyComponent>(
  loader: () => Promise<{ default: Component }>,
): PreloadableLazyComponent<Component> {
  let pending: Promise<{ default: Component }> | undefined;
  const load = () => {
    pending ??= loader().catch((error: unknown) => {
      pending = undefined;
      throw error;
    });
    return pending;
  };
  return Object.assign(lazy(load), { preload: load });
}
