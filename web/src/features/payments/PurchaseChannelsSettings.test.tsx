import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, it, vi } from "vitest";
import { PurchaseChannelsSettings } from "./PurchaseChannelsSettings";

const initial = { revision: 1, card_store_url: "", chatwoot_base_url: "", chatwoot_website_token: "" };
it("saves channel settings with revision and preserves values after a conflict", async () => {
  const api = { getAdminPurchaseChannels: vi.fn().mockResolvedValue(initial), updatePurchaseChannels: vi.fn().mockRejectedValueOnce(new Error("配置已变化，请重新加载")).mockImplementation((value: typeof initial) => Promise.resolve({ ...value, revision: 2 })) };
  const user = userEvent.setup(); render(<PurchaseChannelsSettings api={api} />);
  const input = await screen.findByLabelText("发卡网购买地址");
  await user.type(input, "https://cards.example.test/shop"); await user.click(screen.getByRole("button", { name: "保存购买渠道" }));
  expect(await screen.findByRole("alert")).toHaveTextContent("配置已变化"); expect(input).toHaveValue("https://cards.example.test/shop");
  await user.click(screen.getByRole("button", { name: "保存购买渠道" }));
  await waitFor(() => expect(screen.getByRole("status")).toHaveTextContent("购买渠道已保存"));
  expect(api.updatePurchaseChannels).toHaveBeenLastCalledWith({ ...initial, card_store_url: "https://cards.example.test/shop" });
});
it("locks the submitted draft until saving completes", async () => {
  let finish!: (value: typeof initial) => void;
  const api = { getAdminPurchaseChannels: vi.fn().mockResolvedValue(initial), updatePurchaseChannels: vi.fn().mockReturnValue(new Promise<typeof initial>(resolve => { finish = resolve; })) };
  const user = userEvent.setup(); render(<PurchaseChannelsSettings api={api} />);
  await screen.findByLabelText("发卡网购买地址"); await user.click(screen.getByRole("button", { name: "保存购买渠道" }));
  expect(screen.getByLabelText("发卡网购买地址")).toBeDisabled(); expect(screen.getByLabelText("Chatwoot 网站标识")).toBeDisabled();
  await act(async () => { finish({ ...initial, revision: 2 }); await Promise.resolve(); });
  expect(screen.getByLabelText("发卡网购买地址")).toBeEnabled(); expect(screen.getByRole("status")).toHaveTextContent("购买渠道已保存");
});
