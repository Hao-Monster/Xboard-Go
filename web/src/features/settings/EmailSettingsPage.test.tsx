import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, it, vi } from "vitest";

import type { AdminAPI } from "../../lib/api";
import { EmailSettingsPage } from "./EmailSettingsPage";

vi.mock("./MailSettingsPage", () => ({ MailSettingsPage: () => <h1>SMTP 配置</h1> }));
vi.mock("./MailTemplateSettingsPage", () => ({ MailTemplateSettingsPage: () => <h1>模板编辑器</h1> }));

it("keeps mail settings and templates in the same legacy-compatible tab surface", async () => {
  const user = userEvent.setup();
  render(<EmailSettingsPage api={{} as AdminAPI} />);

  expect(screen.getByRole("tab", { name: "邮件设置" })).toHaveAttribute("aria-selected", "true");
  expect(screen.getByRole("heading", { name: "SMTP 配置" })).toBeVisible();
  await user.click(screen.getByRole("tab", { name: "邮件模板" }));
  expect(screen.getByRole("tab", { name: "邮件模板" })).toHaveAttribute("aria-selected", "true");
  expect(screen.getByRole("heading", { name: "模板编辑器" })).toBeVisible();
});
