import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { UserGiftCardPage } from "./UserGiftCardPage";

const code = "QAFUNCTION123456";
const preview = { template: { name: "功能测试卡" }, reward_preview: { balance: 500 }, can_redeem: true };
const item = (id: number) => ({ id, template_name: `记录${id}`, code: `QA${id}1234567890`, rewards: { balance: 500 }, used_at: "2026-10-04T00:00:00Z", order_trade_no: `ORDER-${id}` });
const pageOf = (total: number, page = 1) => ({ items: Array.from({ length: Math.max(0, Math.min(15, total - (page - 1) * 15)) }, (_, offset) => item(total - (page - 1) * 15 - offset)), total, page, page_size: 15 });
function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: Error) => void;
  const promise = new Promise<T>((accept, fail) => { resolve = accept; reject = fail; });
  return { promise, resolve, reject };
}
function apiFor(total = 0) {
  return {
    checkGiftCard: vi.fn().mockResolvedValue(preview),
    redeemGiftCard: vi.fn().mockResolvedValue({ message: "兑换成功", rewards: { balance: 500 } }),
    listMyGiftCardUsages: vi.fn().mockImplementation((page: number = 1) => Promise.resolve(pageOf(total, page)))
  };
}
async function query(user: ReturnType<typeof userEvent.setup>) {
  await user.type(screen.getByRole("textbox", { name: "礼品卡兑换码" }), code.toLowerCase());
  await user.keyboard("{Enter}");
}

describe("post-fix gift card functional acceptance", () => {
  it.each([0, 1, 15, 16, 30, 31])("PF-14 traverses %i records without duplicates or omissions", async total => {
    const api = apiFor(total);
    const user = userEvent.setup();
    render(<UserGiftCardPage api={api} />);
    await waitFor(() => expect(screen.queryByText("正在加载兑换记录…")).not.toBeInTheDocument());
    if (total === 0) {
      expect(screen.getByText("暂无兑换记录")).toBeVisible();
      expect(screen.queryByRole("table")).not.toBeInTheDocument();
    }
    const seen: number[] = [];
    for (let page = 1; page <= Math.ceil(total / 15); page++) {
      const count = Math.min(15, total - (page - 1) * 15);
      const cells = screen.getAllByText(/^ORDER-\d+$/);
      expect(cells).toHaveLength(count);
      seen.push(...cells.map(cell => Number(cell.textContent?.replace("ORDER-", ""))));
      if (total > 15) {
        expect(screen.getByText(`第 ${page} / ${Math.ceil(total / 15)} 页 · 共 ${total} 条`)).toBeVisible();
        if (page === 1) expect(screen.getByRole("button", { name: "上一页" })).toBeDisabled();
        if (page < Math.ceil(total / 15)) {
          await user.click(screen.getByRole("button", { name: "下一页" }));
          await screen.findByText(`第 ${page + 1} / ${Math.ceil(total / 15)} 页 · 共 ${total} 条`);
        } else expect(screen.getByRole("button", { name: "下一页" })).toBeDisabled();
      }
    }
    expect(seen).toEqual(Array.from({ length: total }, (_, index) => total - index));
    if (total <= 15) expect(screen.queryByRole("navigation", { name: "兑换记录分页" })).not.toBeInTheDocument();
    else {
      for (let page = Math.ceil(total / 15); page > 1; page--) await user.click(screen.getByRole("button", { name: "上一页" }));
      expect(await screen.findByText(`ORDER-${total}`)).toBeVisible();
      expect(api.listMyGiftCardUsages).toHaveBeenLastCalledWith(1, 15);
    }
  });

  it("PF-13 retries an initial history failure before showing a genuine empty state", async () => {
    const api = apiFor();
    api.listMyGiftCardUsages.mockRejectedValueOnce(new Error("历史暂不可用"));
    const user = userEvent.setup();
    render(<UserGiftCardPage api={api} />);
    expect(await screen.findByRole("alert")).toHaveTextContent("历史暂不可用");
    expect(screen.queryByText("暂无兑换记录")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "重试加载记录" }));
    expect(await screen.findByText("暂无兑换记录")).toBeVisible();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(api.listMyGiftCardUsages).toHaveBeenLastCalledWith(1, 15);
  });

  it("PF-09 recovers a current query failure with Enter and never redeems on query", async () => {
    const api = apiFor();
    api.checkGiftCard.mockRejectedValueOnce(new Error("查询断网"));
    const user = userEvent.setup();
    render(<UserGiftCardPage api={api} />);
    await query(user);
    expect(await screen.findByRole("alert")).toHaveTextContent("查询断网");
    expect(screen.queryByRole("button", { name: "确认兑换" })).not.toBeInTheDocument();
    await user.keyboard("{Enter}");
    expect(await screen.findByRole("button", { name: "确认兑换" })).toBeVisible();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(api.checkGiftCard).toHaveBeenLastCalledWith(code);
    expect(api.redeemGiftCard).not.toHaveBeenCalled();
  });

  it("PF-12 preserves redemption success when history fails and retries only the history", async () => {
    const api = apiFor(16);
    api.listMyGiftCardUsages.mockResolvedValueOnce(pageOf(16)).mockResolvedValueOnce(pageOf(16, 2)).mockRejectedValueOnce(new Error("刷新记录失败")).mockResolvedValueOnce(pageOf(17));
    const user = userEvent.setup();
    render(<UserGiftCardPage api={api} />);
    await screen.findByText("ORDER-16");
    await user.click(screen.getByRole("button", { name: "下一页" }));
    await query(user);
    await user.click(await screen.findByRole("button", { name: "确认兑换" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("刷新记录失败");
    expect(screen.getByText(/兑换成功/)).toBeVisible();
    expect(screen.getByText("ORDER-1")).toBeVisible();
    expect(screen.getByRole("textbox", { name: "礼品卡兑换码" })).toHaveValue("");
    expect(screen.getByRole("textbox", { name: "礼品卡兑换码" })).toBeEnabled();
    expect(screen.queryByRole("button", { name: "确认兑换" })).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "重试加载记录" }));
    expect(await screen.findByText("ORDER-17")).toBeVisible();
    expect(api.redeemGiftCard).toHaveBeenCalledExactlyOnceWith(code);
    expect(api.listMyGiftCardUsages).toHaveBeenLastCalledWith(1, 15);
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it.each(["resolve", "reject"] as const)("PF-06 old query %s cannot unlock a newer pending query", async outcome => {
    const old = deferred<typeof preview>();
    const latest = deferred<typeof preview>();
    const api = apiFor();
    api.checkGiftCard.mockReturnValueOnce(old.promise).mockReturnValueOnce(latest.promise);
    const user = userEvent.setup();
    render(<UserGiftCardPage api={api} />);
    await query(user);
    const input = screen.getByRole("textbox", { name: "礼品卡兑换码" });
    await user.clear(input);
    await user.type(input, "LATESTCODE123");
    await user.keyboard("{Enter}");
    await act(async () => {
      if (outcome === "resolve") old.resolve(preview); else old.reject(new Error("旧错误"));
      await old.promise.catch(() => undefined);
    });
    expect(screen.getByRole("button", { name: "查询中…" })).toBeDisabled();
    expect(screen.queryByRole("button", { name: "确认兑换" })).not.toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    await act(async () => { latest.resolve(preview); await latest.promise; });
    expect(await screen.findByRole("button", { name: "确认兑换" })).toBeEnabled();
  });
});
