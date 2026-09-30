import { useState } from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, it } from "vitest";
import { MarkdownEditor } from "./MarkdownEditor";

function Editor() {
  const [value, setValue] = useState("原有正文");
  return <form onSubmit={event => { event.preventDefault(); throw new Error("工具不应提交表单"); }}><MarkdownEditor label="正文" value={value} onChange={setValue} /></form>;
}

it("formats the selection without losing surrounding text and supports undo and redo", async () => {
  const user = userEvent.setup();
  render(<Editor />);
  const field = screen.getByRole<HTMLTextAreaElement>("textbox", { name: "正文" });
  field.focus(); field.setSelectionRange(2, 4);
  await user.click(screen.getByRole("button", { name: "正文：粗体" }));
  expect(field).toHaveValue("原有**正文**");
  await user.click(screen.getByRole("button", { name: "撤销" }));
  expect(field).toHaveValue("原有正文");
  await user.click(screen.getByRole("button", { name: "重做" }));
  expect(field).toHaveValue("原有**正文**");
});

it("supports heading levels, clearing with undo, and a safe preview without submitting", async () => {
  const user = userEvent.setup(); render(<Editor />);
  const field = screen.getByRole<HTMLTextAreaElement>("textbox", { name: "正文" });
  field.focus(); field.setSelectionRange(0, 4);
  await user.click(screen.getByRole("button", { name: "正文：标题" }));
  await user.click(screen.getByRole("button", { name: "H3" }));
  expect(field).toHaveValue("### 原有正文");
  await user.click(screen.getByRole("button", { name: "正文：显示编辑器与预览" }));
  expect(screen.getByRole("heading", { name: "原有正文", level: 3 })).toBeVisible();
  await user.click(screen.getByRole("button", { name: "正文：清空" }));
  expect(field).toHaveValue("");
  await user.click(screen.getByRole("button", { name: "撤销" }));
  expect(field).toHaveValue("### 原有正文");
});
