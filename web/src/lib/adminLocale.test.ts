import { afterEach, expect, it } from "vitest";
import { applyAdminLocale, loadAdminLocale, translateAdmin } from "./adminLocale";

afterEach(() => applyAdminLocale({}));

it("uses the reference dictionaries and restores unchanged Chinese copy", async () => {
  applyAdminLocale(await loadAdminLocale("en-US"));
  expect(translateAdmin("节点管理")).toBe("Node Management");
  expect(translateAdmin("取消")).toBe("Cancel");
  expect(translateAdmin("fixture.example.test")).toBe("fixture.example.test");
  applyAdminLocale(await loadAdminLocale("ru-RU"));
  expect(translateAdmin("节点管理")).toBe("Узлы");
  applyAdminLocale(await loadAdminLocale("zh-CN"));
  expect(translateAdmin("节点管理")).toBe("节点管理");
});

it("preserves whitespace and treats replacement-like symbols as literal text", () => {
  applyAdminLocale({ 金额: "$& $1" });
  expect(translateAdmin(" 金额 ")).toBe(" $& $1 ");
});
