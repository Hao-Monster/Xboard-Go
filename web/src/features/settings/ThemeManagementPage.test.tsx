import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { ThemeCatalog, ThemeItem } from "../../lib/api";
import { ThemeManagementPage } from "./ThemeManagementPage";

const xboard: ThemeItem = {
  name: "Xboard", description: "Built in", version: "1.0.0", images: [], backgrounds: [],
  palettes: {
    default: { background: "#0b0d12", surface: "#151922", text: "#e8ebf2", muted: "#9ba3b5", primary: "#9ab2ff", primary_text: "#101218", border: "#303746" },
    blue: { background: "#0c1426", surface: "#14213a", text: "#e8efff", muted: "#9cabc5", primary: "#8fb5ff", primary_text: "#0a1020", border: "#30466d" },
    black: { background: "#050505", surface: "#111111", text: "#f5f5f5", muted: "#a3a3a3", primary: "#d4d4d4", primary_text: "#0a0a0a", border: "#333333" },
    darkblue: { background: "#07111f", surface: "#0d1b2d", text: "#e5f0ff", muted: "#94a8c3", primary: "#82b7ff", primary_text: "#07111f", border: "#294462" }
  },
  config: { theme_color: "default", background_url: "", font_scale: "normal", radius: "rounded" },
  package_sha256: "0".repeat(64), revision: 1, is_system: true, is_active: true, can_delete: false,
  updated_at: "1970-01-01T00:00:00Z"
};

const aurora: ThemeItem = {
  ...xboard, name: "Aurora", description: "Custom", images: ["assets/preview.png"],
  package_sha256: "a".repeat(64), is_system: false, is_active: false, can_delete: true,
  updated_at: "2026-08-30T12:00:00Z"
};

const catalog: ThemeCatalog = { active_theme: "Xboard", revision: 1, sidebar_style: "light", header_style: "dark", themes: [xboard, aurora] };

describe("ThemeManagementPage", () => {
  it("shows legacy-equivalent actions on first open and keeps the config modal interactive", async () => {
    const user = userEvent.setup();
    const dirty = vi.fn();
    const changed = vi.fn();
    const saved = { ...aurora, revision: 2, config: { ...aurora.config, theme_color: "blue", font_scale: "large" } };
    const api = {
      listThemes: vi.fn().mockResolvedValue(catalog),
	  updateThemeLayout: vi.fn(),
      uploadTheme: vi.fn(), getTheme: vi.fn().mockImplementation((name: string) => Promise.resolve(name === "Xboard" ? xboard : aurora)),
      updateThemeConfig: vi.fn().mockResolvedValue(saved), activateTheme: vi.fn(), deleteTheme: vi.fn()
    };
    render(<ThemeManagementPage api={api} onDirtyChange={dirty} onThemeChanged={changed} />);

    expect(await screen.findByRole("heading", { name: "主题配置" })).toBeVisible();
    expect(screen.getByRole("button", { name: "预览 Aurora" })).toBeEnabled();
    const settingsButton = screen.getByRole("button", { name: "设置 Aurora" });
    expect(settingsButton).toBeEnabled();
    expect(screen.getByRole("button", { name: "激活 Aurora" })).toBeEnabled();
    expect(screen.getByRole("button", { name: "删除 Aurora" })).toBeEnabled();

    const xboardSettingsButton = screen.getByRole("button", { name: "设置 Xboard" });
    await user.click(xboardSettingsButton);
    expect(await screen.findByRole("dialog", { name: "Xboard 主题设置" })).toBeVisible();
    expect(screen.getByLabelText("主题色").querySelectorAll("option")).toHaveLength(4);
    await user.keyboard("{Escape}");
    await waitFor(() => expect(xboardSettingsButton).toHaveFocus());

    await user.click(settingsButton);
    expect(await screen.findByRole("dialog", { name: "Aurora 主题设置" })).toBeVisible();
    await user.selectOptions(screen.getByLabelText("主题色"), "blue");
    await user.selectOptions(screen.getByLabelText("字号"), "large");
    await waitFor(() => expect(dirty).toHaveBeenLastCalledWith(true));
    await user.click(screen.getByRole("button", { name: "保存主题设置" }));
    await waitFor(() => expect(api.updateThemeConfig).toHaveBeenCalledWith("Aurora", {
      revision: 1, theme_color: "blue", background_url: "", font_scale: "large", radius: "rounded"
    }));
    expect(await screen.findByRole("status")).toHaveTextContent("主题设置已保存");
    expect(changed).toHaveBeenCalledTimes(1);
    await waitFor(() => expect(dirty).toHaveBeenLastCalledWith(false));
    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Aurora 主题设置" })).not.toBeInTheDocument());
    await waitFor(() => expect(settingsButton).toHaveFocus());
  });

  it("previews, uploads, activates, protects the current theme, and deletes only after confirmation", async () => {
    const user = userEvent.setup();
    const confirm = vi.spyOn(window, "confirm").mockReturnValueOnce(false).mockReturnValue(true);
    const activated: ThemeCatalog = {
      active_theme: "Aurora", revision: 2, sidebar_style: "light", header_style: "dark",
      themes: [{ ...xboard, is_active: false }, { ...aurora, is_active: true, can_delete: false }]
    };
    const api = {
      listThemes: vi.fn().mockResolvedValue(catalog),
      updateThemeLayout: vi.fn(),
      uploadTheme: vi.fn().mockResolvedValue(aurora), getTheme: vi.fn(), updateThemeConfig: vi.fn(),
      activateTheme: vi.fn().mockResolvedValue(activated), deleteTheme: vi.fn().mockResolvedValue(undefined)
    };
    render(<ThemeManagementPage api={api} />);
    await screen.findByRole("heading", { name: "主题配置" });

    const previewButton = screen.getByRole("button", { name: "预览 Aurora" });
    await user.click(previewButton);
    expect(screen.getByRole("dialog", { name: "Aurora 主题预览" })).toBeVisible();
    expect(screen.getByRole("img", { name: "Aurora 预览 1" })).toHaveAttribute("src", expect.stringContaining("/api/v1/theme-assets/Aurora/"));
    const previewClose = screen.getByRole("button", { name: "关闭预览" });
    await waitFor(() => expect(previewClose).toHaveFocus());
    await user.keyboard("{Tab}");
    expect(previewClose).toHaveFocus();
    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Aurora 主题预览" })).not.toBeInTheDocument());
    await waitFor(() => expect(previewButton).toHaveFocus());

    const file = new File(["zip"], "aurora.zip", { type: "application/zip" });
    fireEvent.change(screen.getByLabelText("上传主题包"), { target: { files: [file] } });
    await waitFor(() => expect(api.uploadTheme).toHaveBeenCalledWith(file));

    await user.click(screen.getByRole("button", { name: "删除 Aurora" }));
    expect(api.deleteTheme).not.toHaveBeenCalled();

    await user.click(screen.getByRole("button", { name: "激活 Aurora" }));
    await waitFor(() => expect(api.activateTheme).toHaveBeenCalledWith("Aurora", 1));
    expect(screen.getByRole("button", { name: "当前主题 Aurora" })).toBeDisabled();
    expect(screen.queryByRole("button", { name: "删除 Aurora" })).not.toBeInTheDocument();

    expect(screen.queryByRole("button", { name: "删除 Xboard" })).not.toBeInTheDocument();
    confirm.mockRestore();
  });

  it("restores the settings trigger after asynchronous loading moved focus away", async () => {
    const user = userEvent.setup();
    let resolveTheme: (item: ThemeItem) => void = () => undefined;
    const api = {
      listThemes: vi.fn().mockResolvedValue(catalog),
	  updateThemeLayout: vi.fn(),
      uploadTheme: vi.fn(),
      getTheme: vi.fn().mockImplementation(() => new Promise<ThemeItem>((resolve) => { resolveTheme = resolve; })),
      updateThemeConfig: vi.fn(), activateTheme: vi.fn(), deleteTheme: vi.fn()
    };
    render(<><button type="button">外部焦点</button><ThemeManagementPage api={api} /></>);
    const settingsButton = await screen.findByRole("button", { name: "设置 Aurora" });

    await user.click(settingsButton);
    screen.getByRole("button", { name: "外部焦点" }).focus();
    act(() => resolveTheme(aurora));
    expect(await screen.findByRole("dialog", { name: "Aurora 主题设置" })).toBeVisible();
    await user.keyboard("{Escape}");

    await waitFor(() => expect(settingsButton).toHaveFocus());
  });

  it("keeps the settings dialog mounted while a save is in flight", async () => {
    const user = userEvent.setup();
    const confirm = vi.spyOn(window, "confirm").mockReturnValue(true);
    let resolveSave: (item: ThemeItem) => void = () => undefined;
    const api = {
      listThemes: vi.fn().mockResolvedValue(catalog), uploadTheme: vi.fn(),
	  updateThemeLayout: vi.fn(),
      getTheme: vi.fn().mockResolvedValue(aurora),
      updateThemeConfig: vi.fn().mockImplementation(() => new Promise<ThemeItem>((resolve) => { resolveSave = resolve; })),
      activateTheme: vi.fn(), deleteTheme: vi.fn()
    };
    render(<ThemeManagementPage api={api} />);
    await user.click(await screen.findByRole("button", { name: "设置 Aurora" }));
    const dialog = await screen.findByRole("dialog", { name: "Aurora 主题设置" });
    await user.selectOptions(screen.getByLabelText("主题色"), "blue");
    await user.click(screen.getByRole("button", { name: "保存主题设置" }));

    expect(screen.getByRole("button", { name: "关闭主题设置" })).toBeDisabled();
    await user.keyboard("{Escape}");
    expect(dialog).toBeVisible();

    act(() => resolveSave({ ...aurora, revision: 2, config: { ...aurora.config, theme_color: "blue" } }));
    expect(await screen.findByRole("status")).toHaveTextContent("主题设置已保存");
    confirm.mockRestore();
  });

  it("updates the legacy-compatible sidebar and header styles with the catalog revision", async () => {
    const user = userEvent.setup();
    const changed = vi.fn();
    const updated = { ...catalog, revision: 2, sidebar_style: "dark" as const };
    const api = {
      listThemes: vi.fn().mockResolvedValue(catalog), updateThemeLayout: vi.fn().mockResolvedValue(updated),
      uploadTheme: vi.fn(), getTheme: vi.fn(), updateThemeConfig: vi.fn(), activateTheme: vi.fn(), deleteTheme: vi.fn()
    };
    render(<ThemeManagementPage api={api} onThemeChanged={changed} />);
    await user.selectOptions(await screen.findByLabelText("侧栏样式"), "dark");
    await waitFor(() => expect(api.updateThemeLayout).toHaveBeenCalledWith(1, "dark", "dark"));
    expect(await screen.findByRole("status")).toHaveTextContent("导航样式已保存");
    expect(changed).toHaveBeenCalledTimes(1);
  });
});
