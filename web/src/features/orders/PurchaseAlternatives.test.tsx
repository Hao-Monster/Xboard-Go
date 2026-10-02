import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, it, vi } from "vitest";
import { PurchaseAlternatives, chatwootWidgetURL, safePurchaseURL } from "./PurchaseAlternatives";

const channels = { revision: 1, card_store_url: "https://cards.example.test/shop", chatwoot_base_url: "https://chat.example.test", chatwoot_website_token: "public_fixture" };
it("offers both configured channels without opening chat or transmitting order data until requested", async () => {
  const redeem = vi.fn(); const user = userEvent.setup();
  render(<PurchaseAlternatives load={vi.fn().mockResolvedValue(channels)} onRedeem={redeem} />);
  expect(await screen.findByRole("link", { name: "前往发卡网购买" })).toHaveAttribute("href", channels.card_store_url);
  expect(screen.getByRole("link", { name: "前往发卡网购买" })).toHaveAttribute("rel", "noopener noreferrer");
  expect(screen.queryByTitle("在线客服聊天")).not.toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "打开客服购买" }));
  const frame = screen.getByTitle("在线客服聊天");
  expect(frame).toHaveAttribute("src", "https://chat.example.test/widget?website_token=public_fixture&locale=zh_CN");
  expect(frame).toHaveAttribute("referrerpolicy", "no-referrer");
  fireEvent.load(frame); expect(screen.getByText("正在加载客服…")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "打开客服购买" })).toBeDisabled();
  await user.click(screen.getByRole("button", { name: "收起客服" })); expect(screen.queryByTitle("在线客服聊天")).not.toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "已有兑换码，去兑换" })); expect(redeem).toHaveBeenCalledOnce();
});
it("shows no fabricated purchase link when both channels are unconfigured", async () => {
  render(<PurchaseAlternatives load={vi.fn().mockResolvedValue({ ...channels, card_store_url: "", chatwoot_base_url: "", chatwoot_website_token: "" })} />);
  expect(await screen.findByText(/当前暂未开放购买渠道/)).toBeVisible();
  expect(screen.queryByRole("link")).not.toBeInTheDocument(); expect(screen.queryByRole("button", { name: "打开客服购买" })).not.toBeInTheDocument();
});
it("only shows configured channels and does not turn load errors into an empty success", async () => {
  const load = vi.fn().mockRejectedValueOnce(new Error("连接失败")).mockResolvedValue({ ...channels, card_store_url: "" });
  const user = userEvent.setup(); render(<PurchaseAlternatives load={load} />);
  expect(await screen.findByRole("alert")).toHaveTextContent("连接失败");
  expect(screen.queryByText(/当前暂未开放购买渠道/)).not.toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "重试购买渠道" }));
  await waitFor(() => expect(screen.getByRole("button", { name: "打开客服购买" })).toBeVisible());
  expect(screen.queryByRole("link", { name: "前往发卡网购买" })).not.toBeInTheDocument();
});
it("rejects script, credentials, invalid widget origins and malformed public identifiers", () => {
  for (const value of ["javascript:alert(1)", "http://cards.example.test", "https://user:secret@cards.example.test", "//evil.test"]) expect(safePurchaseURL(value)).toBeNull();
  expect(chatwootWidgetURL({ ...channels, chatwoot_base_url: "https://chat.example.test/other" })).toBeNull();
  expect(chatwootWidgetURL({ ...channels, chatwoot_website_token: "a&email=other" })).toBeNull();
});
