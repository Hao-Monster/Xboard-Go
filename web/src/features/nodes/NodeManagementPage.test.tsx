import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { NodeManagementPage } from "./NodeManagementPage";

const machine = {
  id: 5, name: "edge-sg", notes: "", is_active: true, last_seen_at: null, load_status: null,
  servers_count: 1, created_at: "2026-08-28T00:00:00Z", updated_at: "2026-08-28T00:00:00Z"
};
const group = { id: 7, name: "Premium", users_count: 1, servers_count: 1, created_at: "2026-08-28T00:00:00Z", updated_at: "2026-08-28T00:00:00Z" };
const node = {
  id: 41, revision: 3, name: "SG VLESS", type: "vless", host: "sg.example.test", port: "443",
  show: true, enabled: true, sort: 10, rate: 2.5, traffic_upload: 1024, traffic_download: 2048,
  runtime_configured: true, last_check_at: null, last_push_at: null, machine_id: 5, machine_name: "edge-sg",
  group_ids: [7], online_count: 4, created_at: "2026-08-28T00:00:00Z", updated_at: "2026-08-28T00:00:00Z"
};

describe("NodeManagementPage", () => {
  it("keeps the legacy node table headers visible when the directory is empty", async () => {
    const api = nodeAPI([]);
    render(<NodeManagementPage api={api} />);

    const table = await screen.findByRole("table", { name: "节点列表" });
    expect(within(table).getByRole("columnheader", { name: "节点ID" })).toBeVisible();
    expect(within(table).getByRole("columnheader", { name: "操作" })).toBeVisible();
    expect(within(table).getByText("没有符合条件的节点。")).toBeVisible();
  });

  it("matches the legacy directory columns, protocol filters, and bounded server pagination", async () => {
    const api = nodeAPI();
    api.listAdminNodes
      .mockResolvedValueOnce({ items: [node], total: 501, page: 1, page_size: 500 })
      .mockResolvedValueOnce({ items: [{ ...node, id: 42, name: "Next node" }], total: 501, page: 2, page_size: 500 });
    const user = userEvent.setup();
    render(<NodeManagementPage api={api} />);

    const table = await screen.findByRole("table", { name: "节点列表" });
    for (const heading of ["节点ID", "显隐", "节点", "部署方式", "地址", "在线人数", "倍率", "权限组", "流量使用", "操作"]) {
      expect(within(table).getByRole("columnheader", { name: heading })).toBeVisible();
    }
    for (const protocol of ["Shadowsocks", "VMess", "Trojan", "Hysteria", "VLess", "TUIC", "SOCKS", "Naive", "HTTP", "Mieru", "AnyTLS"]) {
      expect(screen.getByRole("option", { name: protocol })).toBeInTheDocument();
    }
    expect(within(table).getByText("edge-sg")).toBeVisible();
    expect(within(table).getByText("Premium")).toBeVisible();
    expect(within(table).getByText("4")).toBeVisible();
    expect(api.listAdminNodes).toHaveBeenNthCalledWith(1, { page: 1, page_size: 500 });

    await user.click(screen.getByRole("button", { name: "下一页" }));
    expect(await screen.findByText("Next node")).toBeVisible();
    expect(api.listAdminNodes).toHaveBeenNthCalledWith(2, { page: 2, page_size: 500 });
  });

  it("edits common fields, copies complete nodes, and persists explicit order", async () => {
    const second = { ...node, id: 42, revision: 1, name: "US Trojan", type: "trojan", sort: 20 };
    const api = nodeAPI([node, second]);
    api.updateAdminNode.mockResolvedValue({ ...node, revision: 4, name: "SG VLESS updated" });
    api.copyAdminNode.mockResolvedValue({ ...node, id: 43, revision: 1, name: "SG VLESS - 副本", show: false });
    const user = userEvent.setup();
    render(<NodeManagementPage api={api} />);
    expect(await screen.findByText("US Trojan")).toBeVisible();

    await user.click(screen.getByRole("button", { name: "编辑节点：SG VLESS" }));
    const edit = screen.getByRole("dialog", { name: "编辑节点" });
    await user.clear(within(edit).getByLabelText("节点名称"));
    await user.type(within(edit).getByLabelText("节点名称"), "SG VLESS updated");
    await user.selectOptions(within(edit).getByLabelText("绑定服务器"), "");
    await user.click(within(edit).getByRole("button", { name: "保存修改" }));
    await waitFor(() => expect(api.updateAdminNode).toHaveBeenCalledWith(41, {
      revision: 3, name: "SG VLESS updated", host: "sg.example.test", port: "443",
      show: true, enabled: true, sort: 10, machine_id: null
    }));

    await user.click(screen.getByRole("button", { name: "复制节点：SG VLESS" }));
    await waitFor(() => expect(api.copyAdminNode).toHaveBeenCalledWith(41, 3));
    await user.click(screen.getByRole("button", { name: "上移节点：US Trojan" }));
    await waitFor(() => expect(api.reorderAdminNodes).toHaveBeenCalledWith([{ id: 42, revision: 1 }, { id: 41, revision: 3 }]));
  });

  it("requires confirmation for destructive bulk actions and keeps failures visible", async () => {
    const api = nodeAPI();
    api.deleteAdminNodes.mockRejectedValue(new Error("节点仍被子节点引用"));
    const user = userEvent.setup();
    render(<NodeManagementPage api={api} />);
    expect(await screen.findByText("SG VLESS")).toBeVisible();

    await user.click(screen.getByRole("checkbox", { name: "选择节点：SG VLESS" }));
    await user.click(screen.getByRole("button", { name: "批量停用" }));
    await waitFor(() => expect(api.updateAdminNodeStates).toHaveBeenCalledWith({ targets: [{ id: 41, revision: 3 }], enabled: false }));
    await user.click(screen.getByRole("checkbox", { name: "选择节点：SG VLESS" }));
    await user.click(screen.getByRole("button", { name: "批量删除" }));
    const dialog = screen.getByRole("alertdialog", { name: "删除节点" });
    await user.click(within(dialog).getByRole("button", { name: "确认删除" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent("节点仍被子节点引用");
    expect(api.deleteAdminNodes).toHaveBeenCalledWith([{ id: 41, revision: 3 }]);
  });
});

function nodeAPI(items = [node]) {
  return {
    listAdminNodes: vi.fn().mockResolvedValue({ items, total: items.length, page: 1, page_size: 500 }),
    listMachines: vi.fn().mockResolvedValue([machine]),
    listServerGroups: vi.fn().mockResolvedValue([group]),
    updateAdminNode: vi.fn(), copyAdminNode: vi.fn(), reorderAdminNodes: vi.fn().mockResolvedValue(undefined),
    updateAdminNodeStates: vi.fn().mockResolvedValue(undefined), resetAdminNodeTraffic: vi.fn().mockResolvedValue(undefined),
    deleteAdminNodes: vi.fn()
  };
}
