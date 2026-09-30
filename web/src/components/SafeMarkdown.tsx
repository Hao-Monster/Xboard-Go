import Markdown from "react-markdown";
import remarkGfm from "remark-gfm";

type MarkdownNode = {
  type: string;
  value?: string;
  tagName?: string;
  properties?: Record<string, string>;
  children?: MarkdownNode[];
};

// The legacy toolbar emits <u>. Recognize only that exact, attribute-free pair;
// all other raw HTML remains text under react-markdown's default safe behavior.
function underlineOnly() {
  return function visit(node: MarkdownNode) {
    if (!node.children) return;
    node.children.forEach(visit);
    const output: MarkdownNode[] = [];
    for (let index = 0; index < node.children.length; index++) {
      const child = node.children[index];
      if (!child) continue;
      if (child.type === "raw" && child.value === "<u>") {
        const end = node.children.findIndex((item, position) => position > index && item.type === "raw" && item.value === "</u>");
        if (end > index) {
          output.push({ type: "element", tagName: "u", properties: {}, children: node.children.slice(index + 1, end) });
          index = end;
          continue;
        }
      }
      output.push(child);
    }
    node.children = output;
  };
}

export function SafeMarkdown({ children }: { children: string }) {
  return <Markdown remarkPlugins={[remarkGfm]} rehypePlugins={[underlineOnly]} components={{
    a: ({ node, ...props }) => { void node; return <a {...props} target="_blank" rel="noopener noreferrer" />; },
    img: ({ node, ...props }) => { void node; return <img {...props} loading="lazy" referrerPolicy="no-referrer" />; },
  }}>{children}</Markdown>;
}
