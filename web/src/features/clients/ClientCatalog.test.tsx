import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { AdminClientCatalog, ClientCatalogEntry } from "../../lib/api";
import { ClientCatalogManagementPage } from "./ClientCatalogManagementPage";
import { ClientCatalogPage } from "./ClientCatalogPage";

const adminCatalog: AdminClientCatalog = {
  revision: 1,
  clients: [{ id: "karing", name: "Karing", core: "Sing-box", platforms: [
    { platform: "android", links: { direct: "", qr: "", cloud: "", tutorial: "" } },
    { platform: "ios", links: { direct: "", qr: "", cloud: "", tutorial: "" } }
  ] }]
};

const userCatalog: ClientCatalogEntry[] = [{
  id: "karing", name: "Karing", core: "Sing-box", description: "多平台客户端", featured: false, hwid: true,
  downloads: [
    { platform: "android", source: "github", download_url: "/client-download/karing/android", cloud_url: "/client-link/karing/android/cloud", tutorial_url: "/client-link/karing/android/tutorial" },
    { platform: "ios", source: "app-store", download_url: "/client-download/karing/ios", cloud_url: null, tutorial_url: null }
  ]
}];

describe("client catalog", () => {
  it("lets administrators configure all four action links with one revisioned save", async () => {
    const saved = { ...adminCatalog, revision: 2 };
    const api = { listClientCatalogAdmin: vi.fn().mockResolvedValue(adminCatalog), saveClientCatalog: vi.fn().mockResolvedValue(saved) };
    const user = userEvent.setup();
    render(<ClientCatalogManagementPage api={api} />);

    expect(await screen.findByRole("heading", { name: "客户端管理" })).toBeVisible();
    const android = screen.getByRole("region", { name: "Karing Android" });
    fireEvent.change(within(android).getByLabelText("直接下载"), { target: { value: "https://downloads.example.test/karing.apk" } });
    fireEvent.change(within(android).getByLabelText("扫码下载"), { target: { value: "https://qr.example.test/karing" } });
    fireEvent.change(within(android).getByLabelText("网盘下载"), { target: { value: "https://cloud.example.test/karing" } });
    fireEvent.change(within(android).getByLabelText("使用教程"), { target: { value: "/guide/12/karing" } });
    await user.click(screen.getByRole("button", { name: "保存全部配置" }));
    await waitFor(() => expect(api.saveClientCatalog).toHaveBeenCalledWith(1, {
      karing: {
        android: { direct: "https://downloads.example.test/karing.apk", qr: "https://qr.example.test/karing", cloud: "https://cloud.example.test/karing", tutorial: "/guide/12/karing" },
        ios: { direct: "", qr: "", cloud: "", tutorial: "" }
      }
    }));
  });

  it("filters user cards, switches platforms, exposes secure links, and opens a QR modal", async () => {
    const api = {
      listClientCatalog: vi.fn().mockResolvedValue(userCatalog),
      clientCatalogQR: vi.fn().mockResolvedValue({ download_url: "/client-link/karing/android/qr", qr_code: "data:image/svg+xml;base64,PHN2Zy8+" })
    };
    const user = userEvent.setup();
    render(<ClientCatalogPage api={api} />);
    expect(await screen.findByRole("heading", { name: "客户端下载" })).toBeVisible();
    const card = screen.getByRole("article", { name: "Karing" });
    const direct = within(card).getByRole("link", { name: "直接下载" });
    expect(direct).toHaveAttribute("href", "/client-download/karing/android");
    expect(direct).toHaveAttribute("rel", "noopener noreferrer");
    expect(within(card).getByRole("link", { name: "网盘下载" })).toBeVisible();
    await user.selectOptions(within(card).getByLabelText("选择下载平台"), "ios");
    expect(within(card).queryByRole("link", { name: "网盘下载" })).not.toBeInTheDocument();
    await user.selectOptions(within(card).getByLabelText("选择下载平台"), "android");
    await user.click(within(card).getByRole("button", { name: "扫码下载" }));
    const dialog = await screen.findByRole("dialog", { name: "扫码下载 Karing" });
    expect(within(dialog).getByRole("img", { name: "Karing 下载二维码" })).toHaveAttribute("src", "data:image/svg+xml;base64,PHN2Zy8+");
  });
});
