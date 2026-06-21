import ReactMarkdown, { defaultUrlTransform } from "react-markdown";
import remarkGfm from "remark-gfm";
import type { ChatMessage, SourceRef } from "./types";

type MarkdownNode = { type: string; value?: string; url?: string; children?: MarkdownNode[] };

export function sourceDescription(source: SourceRef): string {
  const parts = [source.title];
  if (source.page_number) parts.push(`p.${source.page_number}`);
  if (source.heading_path) parts.push(source.heading_path);
  parts.push(`lines ${source.start_line}–${source.end_line}`);
  return parts.join(" · ");
}

function remarkCitations() {
  return (tree: MarkdownNode) => {
    const visit = (node: MarkdownNode) => {
      if (!node.children || node.type === "link") return;
      for (let index = 0; index < node.children.length; index++) {
        const child = node.children[index];
        if (child.type !== "text" || !child.value) { visit(child); continue; }
        const parts: MarkdownNode[] = [];
        let offset = 0;
        for (const match of child.value.matchAll(/\[(\d+)\]/g)) {
          if (match.index > offset) parts.push({ type: "text", value: child.value.slice(offset, match.index) });
          parts.push({ type: "link", url: `citation:${match[1]}`, children: [{ type: "text", value: match[0] }] });
          offset = match.index + match[0].length;
        }
        if (!parts.length) continue;
        if (offset < child.value.length) parts.push({ type: "text", value: child.value.slice(offset) });
        node.children.splice(index, 1, ...parts);
        index += parts.length - 1;
      }
    };
    visit(tree);
  };
}

export function AnswerMarkdown({ message, onOpen }: { message: ChatMessage; onOpen(documentId: string, chunkId?: number): void }) {
  return <ReactMarkdown
    remarkPlugins={[remarkGfm, remarkCitations]}
    urlTransform={(url) => url.startsWith("citation:") ? url : defaultUrlTransform(url)}
    components={{ a: ({ href, children }) => {
      const label = href?.startsWith("citation:") ? `[${href.slice("citation:".length)}]` : "";
      const source = message.sources?.find((candidate) => candidate.label === label);
      if (source) return <button type="button" className="citation" title={sourceDescription(source)} onClick={() => onOpen(source.document_id, source.chunk_id)}>{children}</button>;
      if (label) return <span className="citation citation-pending">{children}</span>;
      return <a href={href} target="_blank" rel="noopener noreferrer">{children}</a>;
    } }}
  >{message.content || (message.status === "streaming" ? message.statusText || "Searching the manual…" : "")}</ReactMarkdown>;
}
