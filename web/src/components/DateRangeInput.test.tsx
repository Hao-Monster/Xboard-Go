import { useState } from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, it, vi } from "vitest";
import { DateRangeInput } from "./DateRangeInput";

it("selects a range across months without submitting the form and preserves time", async () => {
 const submitted = vi.fn();
 function Example() {
  const [start, setStart] = useState("2026-01-29T12:30");
  const [end, setEnd] = useState("2026-02-05T18:45");
  return <form onSubmit={submitted}><DateRangeInput start={start} end={end} onStartChange={setStart} onEndChange={setEnd} /></form>;
 }
 const user = userEvent.setup(); render(<Example />);
 await user.click(screen.getByRole("button", {name:"选择优惠券有效期"}));
 await user.click(screen.getByRole("button", {name:"2026-01-31"}));
 await user.click(screen.getByRole("button", {name:"2026-02-07"}));
 expect(screen.getByLabelText("开始时间")).toHaveValue("2026-01-31T12:30");
 expect(screen.getByLabelText("结束时间")).toHaveValue("2026-02-07T18:45");
 expect(submitted).not.toHaveBeenCalled();
 await user.keyboard("{Escape}");
 expect(screen.queryByRole("group", {name:"优惠券有效期日历"})).not.toBeInTheDocument();
});
