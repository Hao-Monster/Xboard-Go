import { useState } from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, it } from "vitest";
import { DateTimeInput } from "./DateTimeInput";
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
