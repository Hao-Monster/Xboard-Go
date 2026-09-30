import { fireEvent, render, screen } from "@testing-library/react";
import { useState } from "react";
import { expect, it } from "vitest";
import { CodeEditor } from "./CodeEditor";

it("edits template source and indents selected lines without changing the rest", () => {
  function Example() { const [value, setValue] = useState("proxies:\n- name: test\nrules: []"); return <CodeEditor label="模板" value={value} onChange={setValue} />; }
  render(<Example />);
  const field = screen.getByRole<HTMLTextAreaElement>("textbox", { name: "模板" });
  field.setSelectionRange(9, 20);
  fireEvent.keyDown(field, { key: "]", ctrlKey: true });
  expect(field).toHaveValue("proxies:\n  - name: test\nrules: []");
  field.setSelectionRange(10, 22);
  fireEvent.keyDown(field, { key: "[", ctrlKey: true });
  expect(field).toHaveValue("proxies:\n- name: test\nrules: []");
});
