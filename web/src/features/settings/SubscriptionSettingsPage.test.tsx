import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { SubscriptionSettings } from "../../lib/api";
import { SubscriptionSettingsPage } from "./SubscriptionSettingsPage";

const initial: SubscriptionSettings = {
  revision: 3,
  path: "s",
  show_info: false,
  show_protocol: true,
  templates: {
    singbox: '{"outbounds":[]}', clash: "proxies: []", clashmeta: "proxies: []\nproxy-groups: []",
    stash: "proxies: []\nrules: []", surge: "[Proxy]", surfboard: "[Proxy]"
  },
  updated_at: "2026-08-26T10:00:00Z"
};

describe("SubscriptionSettingsPage", () => {
  it("edits the legacy subscription path, display switches, and all six templates atomically", async () => {
    const updated = { ...initial, revision: 4, path: "feeds_1", show_info: true, templates: { ...initial.templates, clash: "proxies:\n  - name: edge" } };
    const api = {
      getSubscriptionSettings: vi.fn().mockResolvedValue(initial),
      updateSubscriptionSettings: vi.fn().mockResolvedValue(updated)
    };
    const user = userEvent.setup();
    render(<SubscriptionSettingsPage api={api} />);

    expect(await screen.findByRole("heading", { name: "订阅设置" })).toBeVisible();
    expect(screen.getByLabelText("订阅路径")).toHaveValue("s");
    expect(screen.getByRole("checkbox", { name: "在订阅中展示订阅信息" })).not.toBeChecked();
    expect(screen.getByRole("checkbox", { name: "在线路名称中显示协议名称" })).toBeChecked();
    expect(screen.getByLabelText("Sing-box 订阅模板")).toHaveValue(initial.templates.singbox);

    fireEvent.change(screen.getByLabelText("订阅路径"), { target: { value: "feeds_1" } });
    await user.click(screen.getByRole("checkbox", { name: "在订阅中展示订阅信息" }));
    await user.click(screen.getByRole("button", { name: "Clash" }));
    fireEvent.change(screen.getByLabelText("Clash 订阅模板"), { target: { value: updated.templates.clash } });
    await user.click(screen.getByRole("button", { name: "保存订阅设置" }));

    await waitFor(() => expect(api.updateSubscriptionSettings).toHaveBeenCalledWith({
      revision: 3, path: "feeds_1", show_info: true, show_protocol: true,
      templates: { ...initial.templates, clash: updated.templates.clash }
    }));
    expect(await screen.findByRole("status")).toHaveTextContent("订阅设置已保存");
    expect(screen.getByText("Revision 4")).toBeVisible();
  });

  it("keeps unsaved text available after a revision conflict", async () => {
    const api = {
      getSubscriptionSettings: vi.fn().mockResolvedValue(initial),
      updateSubscriptionSettings: vi.fn().mockRejectedValue(new Error("订阅设置已被其他管理员修改，请刷新后重试"))
    };
    const user = userEvent.setup();
    render(<SubscriptionSettingsPage api={api} />);
    const path = await screen.findByLabelText("订阅路径");
    fireEvent.change(path, { target: { value: "draft_path" } });
    await user.click(screen.getByRole("button", { name: "保存订阅设置" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("订阅设置已被其他管理员修改");
    expect(path).toHaveValue("draft_path");
    expect(screen.getByRole("button", { name: "刷新最新设置" })).toBeVisible();
  });
});
