import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, it, vi } from "vitest";
import { NodeGroupCreator } from "./NodeGroupCreator";

it("creates a group inline, reports failures, and returns the created group to the node draft", async () => {
  const group = { id: 7, name: "Premium", users_count: 0, servers_count: 0, created_at: "", updated_at: "" };
  const create = vi.fn().mockRejectedValueOnce(new Error("名称已存在")).mockResolvedValue(group);
  const onCreated = vi.fn();
  const user = userEvent.setup();
  render(<NodeGroupCreator create={create} onCreated={onCreated} />);
  await user.click(screen.getByRole("button", { name: "添加权限组" }));
  expect(screen.getByRole("button", { name: "确认添加" })).toBeDisabled();
  await user.type(screen.getByLabelText("权限组名称"), " Premium ");
  await user.click(screen.getByRole("button", { name: "确认添加" }));
  expect(await screen.findByRole("alert")).toHaveTextContent("名称已存在");
  expect(onCreated).not.toHaveBeenCalled();
  await user.click(screen.getByRole("button", { name: "确认添加" }));
  expect(create).toHaveBeenLastCalledWith("Premium");
  expect(onCreated).toHaveBeenCalledWith(group);
  expect(screen.queryByLabelText("权限组名称")).not.toBeInTheDocument();
});
