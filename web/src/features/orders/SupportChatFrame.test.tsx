import { act, render, screen } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import { SupportChatFrame } from "./SupportChatFrame";

const url = "https://chat.example.test/widget?website_token=public_fixture&locale=zh_CN";
beforeEach(() => sessionStorage.clear());

it("accepts only the configured frame and resumes the same customer's conversation", () => {
  const view = render(<SupportChatFrame url={url} customerID={12} />);
  const frame = screen.getByTitle<HTMLIFrameElement>("在线客服聊天");
  const post = vi.spyOn(frame.contentWindow!, "postMessage");
  const message = JSON.stringify({ event: "loaded", config: { authToken: "fixture.session.signature" } });
  const send = (origin: string, source: Window | null, data = `chatwoot-widget:${message}`) => act(() => { window.dispatchEvent(new MessageEvent("message", { origin, source, data })); });
  send("https://evil.example.test", frame.contentWindow);
  send("https://chat.example.test", window);
  send("https://chat.example.test", frame.contentWindow, "chatwoot-widget:{malformed");
  expect(screen.getByRole("status")).toHaveTextContent("正在加载客服");
  expect(sessionStorage.length).toBe(0); expect(post).not.toHaveBeenCalled();
  send("https://chat.example.test", frame.contentWindow);
  expect(screen.queryByRole("status")).not.toBeInTheDocument();
  expect(post).toHaveBeenCalledWith(expect.stringContaining('"event":"config-set"'), "https://chat.example.test");
  expect(frame).toHaveAttribute("src", url);
  expect(screen.getByRole("link")).toHaveAttribute("href", `${url}&cw_conversation=fixture.session.signature`);
  view.unmount();
  const restored = render(<SupportChatFrame url={url} customerID={12} />);
  expect(screen.getByTitle("在线客服聊天")).toHaveAttribute("src", `${url}&cw_conversation=fixture.session.signature`);
  restored.unmount();
  render(<SupportChatFrame url={url} customerID={13} />);
  expect(screen.getByTitle("在线客服聊天")).toHaveAttribute("src", url);
});
