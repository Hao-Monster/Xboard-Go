import { createElement, useMemo, type ReactNode } from "react";

const allowed = new Set(["p", "div", "span", "br", "strong", "b", "em", "i", "u", "small", "ul", "ol", "li", "a"]);
const blocked = new Set(["script", "style", "iframe", "object", "embed", "svg", "math", "template"]);

export function ThemeFooter({ html }: { html?: string }) {
  const content = useMemo(() => {
    if (!html) return null;
    const document = new DOMParser().parseFromString(html, "text/html");
    const render = (node: Node, key: number): ReactNode => {
      if (node.nodeType === Node.TEXT_NODE) return node.textContent;
      if (!(node instanceof Element)) return null;
      const tag = node.tagName.toLowerCase();
      if (blocked.has(tag)) return null;
      const children = Array.from(node.childNodes).map(render);
      if (!allowed.has(tag)) return children;
      const props: {key:number; href?:string; title?:string; rel?:string} = { key };
      if (tag === "a") {
        const href = node.getAttribute("href") ?? "";
        try { const parsed = new URL(href, window.location.origin); if (["https:", "http:", "mailto:"].includes(parsed.protocol) && !parsed.username && !parsed.password) props.href = href; } catch { /* Invalid links render as inert text. */ }
        props.title = node.getAttribute("title") ?? undefined;
        props.rel = "noopener noreferrer";
      }
      return createElement(tag, props, ...(tag === "br" ? [] : children));
    };
    return Array.from(document.body.childNodes).map(render);
  }, [html]);
  return content ? <footer className="custom-theme-footer">{content}</footer> : null;
}
