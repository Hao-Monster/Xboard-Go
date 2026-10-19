import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { UserGiftCardPage } from "./UserGiftCardPage";

const codeA = "QACODEAAAAAAAA";
const codeB = "QACODEBBBBBBBB";
const preview = (name: string) => ({ template: { name }, reward_preview: { balance: 500 }, can_redeem: true });
const usage = (id: number) => ({ id, template_name: `卡片${id}`, code: `QACODE${id}12345678`, rewards: { balance: 500 }, used_at: "2026-10-04T00:00:00Z", order_trade_no: `ORDER${id}` });
const history = (page = 1) => ({ items: page === 1 ? Array.from({ length: 15 }, (_, index) => usage(16 - index)) : [usage(1)], total: 16, page, page_size: 15 });
function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: Error) => void;
  const promise = new Promise<T>((accept, fail) => { resolve = accept; reject = fail; });
  return { promise, resolve, reject };
}
function makeAPI() {
  return {
    checkGiftCard: vi.fn().mockResolvedValue(preview("卡片预览")),
    redeemGiftCard: vi.fn().mockResolvedValue({ message: "兑换成功", rewards: { balance: 500 } }),
    listMyGiftCardUsages: vi.fn().mockImplementation((page: number = 1) => Promise.resolve(history(page)))
  };
}
async function query(user: ReturnType<typeof userEvent.setup>, code = codeA) {
  const input = screen.getByRole("textbox", { name: "礼品卡兑换码" });
  await user.clear(input);
  await user.type(input, code);
  await user.click(screen.getByRole("button", { name: "查询奖励" }));
}

describe("gift card confirmation and history regressions", () => {
  it("discards an A response after editing to unqueried B, even after editing back to A", async () => {
    const request = deferred<ReturnType<typeof preview>>();
    const api = makeAPI();
    api.checkGiftCard.mockReturnValueOnce(request.promise);
    const user = userEvent.setup();
    render(<UserGiftCardPage api={api} />);
    await query(user);
    const input = screen.getByRole("textbox", { name: "礼品卡兑换码" });
    await user.clear(input);
    await user.type(input, codeB);
    await user.clear(input);
    await user.type(input, codeA);
    await act(async () => { request.resolve(preview("失效的A预览")); await request.promise; });
    expect(screen.queryByText("失效的A预览")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "确认兑换" })).not.toBeInTheDocument();
    expect(api.redeemGiftCard).not.toHaveBeenCalled();
    await query(user, codeB);
    await user.click(await screen.findByRole("button", { name: "确认兑换" }));
    expect(api.redeemGiftCard).toHaveBeenCalledExactlyOnceWith(codeB);
  });

  it.each(["resolve", "reject"] as const)("ignores a superseded query's late %s without replacing the B preview", async (outcome) => {
    const request = deferred<ReturnType<typeof preview>>();
    const api = makeAPI();
    api.checkGiftCard.mockReturnValueOnce(request.promise).mockResolvedValueOnce(preview("B的预览"));
    const user = userEvent.setup();
    render(<UserGiftCardPage api={api} />);
    await query(user);
    await query(user, codeB);
    expect(await screen.findByText("B的预览")).toBeVisible();
    await act(async () => {
      if (outcome === "resolve") request.resolve(preview("过期A")); else request.reject(new Error("过期错误"));
      await request.promise.catch(() => undefined);
    });
    expect(screen.getByText("B的预览")).toBeVisible();
    expect(screen.queryByText("过期错误")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "确认兑换" }));
    expect(api.redeemGiftCard).toHaveBeenCalledExactlyOnceWith(codeB);
  });

  it("invalidates a previous preview while rechecking and locks input and duplicate redemption until completion", async () => {
    const recheck = deferred<ReturnType<typeof preview>>();
    const redeem = deferred<{ message: string; rewards: { balance: number } }>();
    const api = makeAPI();
    api.checkGiftCard.mockResolvedValueOnce(preview("旧预览")).mockReturnValueOnce(recheck.promise);
    api.redeemGiftCard.mockReturnValueOnce(redeem.promise);
    const user = userEvent.setup();
    render(<UserGiftCardPage api={api} />);
    await query(user);
    await screen.findByRole("button", { name: "确认兑换" });
    await user.click(screen.getByRole("button", { name: "查询奖励" }));
    expect(screen.queryByRole("button", { name: "确认兑换" })).not.toBeInTheDocument();
    await act(async () => { recheck.resolve(preview("新预览")); await recheck.promise; });
    await user.dblClick(screen.getByRole("button", { name: "确认兑换" }));
    expect(api.redeemGiftCard).toHaveBeenCalledExactlyOnceWith(codeA);
    expect(screen.getByRole("textbox", { name: "礼品卡兑换码" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "查询奖励" })).toBeDisabled();
    await act(async () => { redeem.reject(new Error("网络中断")); await redeem.promise.catch(() => undefined); });
    expect(await screen.findByRole("alert")).toHaveTextContent("网络中断");
    expect(screen.getByRole("textbox", { name: "礼品卡兑换码" })).toBeEnabled();
    await user.click(screen.getByRole("button", { name: "确认兑换" }));
    expect(api.redeemGiftCard).toHaveBeenCalledTimes(2);
    await waitFor(() => expect(screen.getByRole("textbox", { name: "礼品卡兑换码" })).toHaveValue(""));
  });

  it("reaches records beyond 15, keeps the current page on failure, and retries the requested page", async () => {
    const api = makeAPI();
    api.listMyGiftCardUsages.mockResolvedValueOnce(history()).mockRejectedValueOnce(new Error("翻页失败"));
    const user = userEvent.setup();
    render(<UserGiftCardPage api={api} />);
    await screen.findByText("ORDER16");
    expect(screen.getByRole("button", { name: "上一页" })).toBeDisabled();
    await user.click(screen.getByRole("button", { name: "下一页" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("翻页失败");
    expect(screen.getByText("ORDER16")).toBeVisible();
    await user.click(screen.getByRole("button", { name: "重试加载记录" }));
    expect(await screen.findByText("ORDER1")).toBeVisible();
    expect(api.listMyGiftCardUsages).toHaveBeenLastCalledWith(2, 15);
    expect(screen.getByText("第 2 / 2 页 · 共 16 条")).toBeVisible();
    expect(screen.getByRole("button", { name: "下一页" })).toBeDisabled();
    await user.click(screen.getByRole("button", { name: "上一页" }));
    expect(await screen.findByText("ORDER16")).toBeVisible();
    expect(api.listMyGiftCardUsages).toHaveBeenLastCalledWith(1, 15);
  });

  it("refreshes page one after redeeming and ignores an older pending history response", async () => {
    const oldPage = deferred<ReturnType<typeof history>>();
    const api = makeAPI();
    api.listMyGiftCardUsages.mockResolvedValueOnce(history()).mockReturnValueOnce(oldPage.promise).mockResolvedValueOnce({ ...history(), items: [usage(17)], total: 17 });
    const user = userEvent.setup();
    render(<UserGiftCardPage api={api} />);
    await screen.findByText("ORDER16");
    await query(user);
    await user.click(screen.getByRole("button", { name: "下一页" }));
    expect(screen.getByRole("button", { name: "下一页" })).toBeDisabled();
    await user.click(screen.getByRole("button", { name: "确认兑换" }));
    expect(await screen.findByText("ORDER17")).toBeVisible();
    await act(async () => { oldPage.resolve(history(2)); await oldPage.promise; });
    expect(screen.getByText("ORDER17")).toBeVisible();
    expect(screen.queryByText("ORDER1")).not.toBeInTheDocument();
    expect(api.listMyGiftCardUsages).toHaveBeenLastCalledWith(1, 15);
    const table = screen.getByRole("table", { name: "兑换记录" });
    expect(within(table).getByRole("columnheader", { name: "关联订单" })).toBeVisible();
  });
});
