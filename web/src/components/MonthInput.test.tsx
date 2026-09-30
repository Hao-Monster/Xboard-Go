import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, it, vi } from "vitest";
import { MonthInput } from "./MonthInput";

it("selects across years, clears the filter and cancels without changing its value", async () => {
  const change = vi.fn(); const user = userEvent.setup();
  render(<MonthInput label="结算月份" value="2026-12" onChange={change} />);
  const button = screen.getByRole("button", { name: "结算月份" });
  await user.click(button);
  let picker = within(screen.getByRole("dialog"));
  expect(picker.getByRole("button", { name: "12月" })).toHaveAttribute("aria-pressed", "true");
  await user.click(picker.getByRole("button", { name: "下一年" }));
  await user.click(picker.getByRole("button", { name: "1月" }));
  expect(change).toHaveBeenCalledExactlyOnceWith("2027-01");
  expect(button).toHaveFocus();
  await user.click(button); await user.keyboard("{Escape}");
  expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  expect(change).toHaveBeenCalledTimes(1);
  await user.click(button); picker = within(screen.getByRole("dialog"));
  await user.click(picker.getByRole("button", { name: "清除" }));
  expect(change).toHaveBeenLastCalledWith("");
});
