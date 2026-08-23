import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { RoutingRule, ServerGroup } from "../../lib/api";
import { RoutingRulesPage } from "./RoutingRulesPage";
import { ServerGroupsPage } from "./ServerGroupsPage";

const group: ServerGroup = {
  id: 7, name: "Premium", users_count: 3, server_count: 2,
  created_at: "2026-08-24T00:00:00Z", updated_at: "2026-08-24T00:00:00Z"
};

const rule: RoutingRule = {
  id: 11, remarks: "Domestic direct", match: ["example.cn", "10.0.0.0/8"], action: "direct", action_value: "",
  created_at: "2026-08-24T00:00:00Z", updated_at: "2026-08-24T00:00:00Z"
};

describe("ServerGroupsPage", () => {
  it("creates, edits, and deletes groups while preserving server failures", async () => {
    const created = { ...group, id: 8, name: "New group", users_count: 0, server_count: 0 };
    const updated = { ...group, name: "Premium renamed" };
    const api = {
      listServerGroups: vi.fn().mockResolvedValue([group]),
      createServerGroup: vi.fn().mockResolvedValue(created),
      updateServerGroup: vi.fn().mockResolvedValue(updated),
      deleteServerGroup: vi.fn().mockRejectedValueOnce(new Error("权限组仍被用户使用")).mockResolvedValue(undefined)
    };
    const user = userEvent.setup();
    render(<ServerGroupsPage api={api} />);

    expect(await screen.findByText("Premium")).toBeVisible();
    expect(screen.getByRole("region", { name: "权限组列表" })).toHaveTextContent("3");

    await user.click(screen.getByRole("button", { name: "新增权限组" }));
    let dialog = screen.getByRole("dialog", { name: "新增权限组" });
    await user.type(within(dialog).getByLabelText("权限组名称"), "New group");
    await user.click(within(dialog).getByRole("button", { name: "保存" }));
    await waitFor(() => expect(api.createServerGroup).toHaveBeenCalledWith("New group"));
    expect(await screen.findByText("New group")).toBeVisible();

    await user.click(screen.getByRole("button", { name: "编辑权限组：Premium" }));
    dialog = screen.getByRole("dialog", { name: "编辑权限组" });
    const name = within(dialog).getByLabelText("权限组名称");
    await user.clear(name);
    await user.type(name, "Premium renamed");
    dialog = screen.getByRole("dialog", { name: "编辑权限组" });
    await user.click(within(dialog).getByRole("button", { name: "保存" }));
    await waitFor(() => expect(api.updateServerGroup).toHaveBeenCalledWith(7, "Premium renamed"));
    expect(await screen.findByText("Premium renamed")).toBeVisible();

    await user.click(screen.getByRole("button", { name: "删除权限组：Premium renamed" }));
    dialog = screen.getByRole("dialog", { name: "删除权限组" });
    await user.click(within(dialog).getByRole("button", { name: "确认删除" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent("权限组仍被用户使用");
    expect(dialog).toBeVisible();
    await user.click(within(dialog).getByRole("button", { name: "确认删除" }));
    await waitFor(() => expect(screen.queryByText("Premium renamed")).not.toBeInTheDocument());
  });

  it("shows a retriable load error without a false empty state", async () => {
    const api = {
      listServerGroups: vi.fn().mockRejectedValueOnce(new Error("加载失败")).mockResolvedValue([]),
      createServerGroup: vi.fn(), updateServerGroup: vi.fn(), deleteServerGroup: vi.fn()
    };
    const user = userEvent.setup();
    render(<ServerGroupsPage api={api} />);
    expect(await screen.findByRole("alert")).toHaveTextContent("加载失败");
    await user.click(screen.getByRole("button", { name: "重试" }));
    expect(await screen.findByText("尚未创建权限组。")).toBeVisible();
  });
});

describe("RoutingRulesPage", () => {
  it("creates proxy rules, filters them, and clears stale targets when editing to direct", async () => {
    const created: RoutingRule = { ...rule, id: 12, remarks: "Proxy overseas", match: ["*.example.com", "geoip:us"], action: "proxy", action_value: "warp-out" };
    const updated: RoutingRule = { ...created, action: "direct", action_value: "" };
    const api = {
      listRoutingRules: vi.fn().mockResolvedValue([rule]),
      createRoutingRule: vi.fn().mockResolvedValue(created),
      updateRoutingRule: vi.fn().mockResolvedValue(updated),
      deleteRoutingRule: vi.fn().mockResolvedValue(undefined)
    };
    const user = userEvent.setup();
    render(<RoutingRulesPage api={api} />);
    expect(await screen.findByText("Domestic direct")).toBeVisible();

    await user.click(screen.getByRole("button", { name: "新增路由规则" }));
    let dialog = screen.getByRole("dialog", { name: "新增路由规则" });
    await user.type(within(dialog).getByLabelText("备注"), "Proxy overseas");
    dialog = screen.getByRole("dialog", { name: "新增路由规则" });
    await user.type(within(dialog).getByLabelText("匹配规则"), "*.example.com{enter}geoip:us");
    dialog = screen.getByRole("dialog", { name: "新增路由规则" });
    await user.selectOptions(within(dialog).getByLabelText("动作"), "proxy");
    dialog = screen.getByRole("dialog", { name: "新增路由规则" });
    await user.type(within(dialog).getByLabelText("代理出站标记"), "warp-out");
    dialog = screen.getByRole("dialog", { name: "新增路由规则" });
    await user.click(within(dialog).getByRole("button", { name: "保存" }));
    await waitFor(() => expect(api.createRoutingRule).toHaveBeenCalledWith({
      remarks: "Proxy overseas", match: ["*.example.com", "geoip:us"], action: "proxy", action_value: "warp-out"
    }));
    expect(await screen.findByText("Proxy overseas")).toBeVisible();

    const search = screen.getByRole("searchbox", { name: "搜索规则" });
    await user.type(search, "geoip:us");
    expect(screen.queryByText("Domestic direct")).not.toBeInTheDocument();
    await user.clear(search);

    await user.click(screen.getByRole("button", { name: "编辑路由规则：Proxy overseas" }));
    dialog = screen.getByRole("dialog", { name: "编辑路由规则" });
    await user.selectOptions(within(dialog).getByLabelText("动作"), "direct");
    dialog = screen.getByRole("dialog", { name: "编辑路由规则" });
    expect(within(dialog).queryByLabelText("代理出站标记")).not.toBeInTheDocument();
    await user.click(within(dialog).getByRole("button", { name: "保存" }));
    await waitFor(() => expect(api.updateRoutingRule).toHaveBeenCalledWith(12, {
      remarks: "Proxy overseas", match: ["*.example.com", "geoip:us"], action: "direct", action_value: ""
    }));

    await user.click(screen.getByRole("button", { name: "删除路由规则：Proxy overseas" }));
    dialog = screen.getByRole("dialog", { name: "删除路由规则" });
    await user.click(within(dialog).getByRole("button", { name: "确认删除" }));
    await waitFor(() => expect(api.deleteRoutingRule).toHaveBeenCalledWith(12));
  });
});
