import { render, screen } from "@testing-library/react";
import { Suspense } from "react";
import { describe, expect, it, vi } from "vitest";

import { lazyWithPreload } from "./lazyWithPreload";

describe("lazyWithPreload", () => {
  it("shares one module request between proactive preload and React.lazy", async () => {
    const loader = vi.fn(() => Promise.resolve({ default: () => <p>预加载页面</p> }));
    const Page = lazyWithPreload(loader);

    await Promise.all([Page.preload(), Page.preload()]);
    render(<Suspense fallback={<p>正在加载</p>}><Page /></Suspense>);

    expect(await screen.findByText("预加载页面")).toBeVisible();
    expect(screen.queryByText("正在加载")).not.toBeInTheDocument();
    expect(loader).toHaveBeenCalledTimes(1);
  });

  it("retries after a proactive preload fails", async () => {
    const loader = vi.fn()
      .mockRejectedValueOnce(new Error("temporary chunk failure"))
      .mockResolvedValue({ default: () => <p>重试成功</p> });
    const Page = lazyWithPreload(loader);

    await expect(Page.preload()).rejects.toThrow("temporary chunk failure");
    await Page.preload();
    render(<Suspense fallback={<p>正在加载</p>}><Page /></Suspense>);

    expect(await screen.findByText("重试成功")).toBeVisible();
    expect(loader).toHaveBeenCalledTimes(2);
  });
});
