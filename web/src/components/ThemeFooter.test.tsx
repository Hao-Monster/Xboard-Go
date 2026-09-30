import { render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { ThemeFooter } from "./ThemeFooter";
it("renders allowed footer markup while dropping executable elements and attributes", () => {
 const {container} = render(<ThemeFooter html={'<p onclick="alert(1)">支持 <strong>全天服务</strong><a href="https://example.test/help">帮助</a><a href="javascript:alert(1)">危险</a><script>secret-script</script><iframe src="https://example.test"></iframe></p>'} />);
 expect(screen.getByText("全天服务").tagName).toBe("STRONG");
 expect(screen.getByRole("link", {name:"帮助"})).toHaveAttribute("href", "https://example.test/help");
 expect(screen.getByText("危险")).not.toHaveAttribute("href");
 expect(container.querySelector("script, iframe, [onclick]")).toBeNull();
 expect(container).not.toHaveTextContent("secret-script");
});
