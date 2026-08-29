import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { ClientAppSettings } from "../../lib/api";
import { ClientAppSettingsPage } from "./ClientAppSettingsPage";

const initial: ClientAppSettings = {
  revision: 4,
  windows_version: "4.8.1", windows_download_url: "https://download.example.test/windows.exe",
  macos_version: "4.8.2", macos_download_url: "https://download.example.test/macos.dmg",
  android_version: "4.8.3", android_download_url: "https://download.example.test/android.apk",
  updated_at: "2026-08-30T11:00:00Z"
};

describe("ClientAppSettingsPage", () => {
  it("shows all six legacy fields on first open and saves one complete revisioned update", async () => {
    const user = userEvent.setup();
    const dirty = vi.fn();
    const updated = { ...initial, revision: 5, windows_version: "5.0.0" };
    let resolveUpdate!: (settings: ClientAppSettings) => void;
    const updatePromise = new Promise<ClientAppSettings>((resolve) => { resolveUpdate = resolve; });
    const api = {
      getClientAppSettings: vi.fn().mockResolvedValue(initial),
      updateClientAppSettings: vi.fn().mockReturnValue(updatePromise)
    };
    render(<ClientAppSettingsPage api={api} onDirtyChange={dirty} />);

    expect(await screen.findByRole("heading", { name: "客户端版本" })).toBeVisible();
    expect(screen.getByLabelText("Windows 版本")).toHaveValue("4.8.1");
    expect(screen.getByLabelText("Windows 下载地址")).toHaveValue("https://download.example.test/windows.exe");
    expect(screen.getByLabelText("macOS 版本")).toHaveValue("4.8.2");
    expect(screen.getByLabelText("macOS 下载地址")).toHaveValue("https://download.example.test/macos.dmg");
    expect(screen.getByLabelText("Android 版本")).toHaveValue("4.8.3");
    expect(screen.getByLabelText("Android 下载地址")).toHaveValue("https://download.example.test/android.apk");
    expect(screen.getByRole("button", { name: "保存客户端版本" })).toBeDisabled();

    fireEvent.change(screen.getByLabelText("Windows 版本"), { target: { value: "5.0.0" } });
    await waitFor(() => expect(dirty).toHaveBeenLastCalledWith(true));
    expect(screen.getByRole("button", { name: "保存客户端版本" })).toBeEnabled();
    await user.click(screen.getByRole("button", { name: "保存客户端版本" }));

    await waitFor(() => expect(api.updateClientAppSettings).toHaveBeenCalledWith({
      revision: 4,
      windows_version: "5.0.0", windows_download_url: initial.windows_download_url,
      macos_version: initial.macos_version, macos_download_url: initial.macos_download_url,
      android_version: initial.android_version, android_download_url: initial.android_download_url
    }));
    expect(screen.getByLabelText("Windows 版本")).toBeDisabled();
    resolveUpdate(updated);
    expect(await screen.findByRole("status")).toHaveTextContent("客户端版本设置已保存");
    await waitFor(() => expect(dirty).toHaveBeenLastCalledWith(false));
  });

  it("keeps a failed save editable and can recover from an initial load failure", async () => {
    const user = userEvent.setup();
    const api = {
      getClientAppSettings: vi.fn().mockRejectedValueOnce(new Error("客户端版本暂时不可用")).mockResolvedValue(initial),
      updateClientAppSettings: vi.fn().mockRejectedValue(new Error("设置已被其他管理员修改"))
    };
    render(<ClientAppSettingsPage api={api} />);

    expect(await screen.findByRole("alert")).toHaveTextContent("客户端版本暂时不可用");
    await user.click(screen.getByRole("button", { name: "重新加载客户端版本设置" }));
    expect(await screen.findByLabelText("Windows 版本")).toHaveValue("4.8.1");
    fireEvent.change(screen.getByLabelText("Windows 版本"), { target: { value: "5.0.0" } });
    await user.click(screen.getByRole("button", { name: "保存客户端版本" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("设置已被其他管理员修改");
    expect(screen.getByLabelText("Windows 版本")).toHaveValue("5.0.0");
  });
});
