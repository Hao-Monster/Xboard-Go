import { render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { SafeMarkdown } from "./SafeMarkdown";

it("renders toolbar table, strike and underline syntax without enabling executable HTML", () => {
  const { container } = render(<SafeMarkdown>{'| Name | Value |\n| --- | --- |\n| Plan | 10 |\n\n~~Removed~~ and <u>Underlined</u>\n\n<script>alert(1)</script>\n\n<u onclick="alert(1)">unsafe</u>\n\n[bad](javascript:alert(1))'}</SafeMarkdown>);
  expect(screen.getByRole("table")).toBeVisible();
  expect(screen.getByText("Removed").tagName).toBe("DEL");
  expect(screen.getByText("Underlined").tagName).toBe("U");
  expect(container.querySelector("script, [onclick]")).toBeNull();
  expect(container.querySelector('a[href^="javascript:"]')).toBeNull();
});
