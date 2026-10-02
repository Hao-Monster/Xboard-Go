import { useState } from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, it } from "vitest";
import { DateTimeInput } from "./DateTimeInput";
import { Modal } from "./Overlay";
function Example() { const [value, setValue] = useState("2026-08-01T13:45"); return <><DateTimeInput label="到期时间" value={value} onChange={setValue} /><output>{value}</output></>; }
it("chooses a calendar date preserving time and can restore unlimited expiry", async () => {
 const user = userEvent.setup(); render(<Example />);
 await user.click(screen.getByRole("button", { name: "到期时间" }));
 await user.click(screen.getByRole("button", { name: "2026-08-12" }));
 expect(screen.getByRole("status")).toHaveTextContent("2026-08-12T13:45");
 await user.click(screen.getByRole("button", { name: "到期时间" }));
 await user.click(screen.getByRole("button", { name: "长期有效" }));
 expect(screen.getByRole("status")).toBeEmptyDOMElement();
});
it("positions the calendar inside the viewport and closes it before its parent dialog", async () => {
 const user = userEvent.setup();
 function DialogExample() { const [open, setOpen] = useState(true); return open ? <Modal title="日期测试" onClose={() => setOpen(false)}><Example /></Modal> : null; }
 render(<DialogExample />);
 const trigger = screen.getByRole("button", { name: "到期时间" });
 await user.click(trigger);
 const calendar = screen.getByRole("group", { name: "到期时间日历" });
 expect(calendar.style.top).not.toBe(""); expect(calendar.style.left).not.toBe("");
 await user.keyboard("{Escape}");
 expect(screen.queryByRole("group", { name: "到期时间日历" })).not.toBeInTheDocument();
 expect(screen.getByRole("dialog", { name: "日期测试" })).toBeVisible(); expect(trigger).toHaveFocus();
 await user.click(trigger); await user.click(screen.getByRole("status"));
 expect(screen.queryByRole("group", { name: "到期时间日历" })).not.toBeInTheDocument();
});
