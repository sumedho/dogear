import type { AskResult, DocumentInfo } from "./types";

async function json<T>(path: string, options?: RequestInit): Promise<T> {
  const response = await fetch(path, options);
  const payload = (await response.json()) as T & { error?: string };
  if (!response.ok) throw new Error(payload.error || response.statusText);
  return payload;
}

export function listDocuments(): Promise<DocumentInfo[]> {
  return json<DocumentInfo[]>("/api/documents");
}

export async function importMarkdown(file: File, replace: boolean): Promise<{ documents: number; chunks: number }> {
  const form = new FormData();
  form.set("file", file);
  form.set("replace", String(replace));
  return json("/api/import", { method: "POST", body: form });
}

export interface StreamHandlers {
  onDelta(content: string): void;
  onResult(result: AskResult): void;
}

export class SSEParser {
  private buffer = "";

  push(chunk: string, emit: (event: string, data: string) => void): void {
    this.buffer = (this.buffer + chunk).replaceAll("\r\n", "\n");
    let boundary = this.buffer.indexOf("\n\n");
    while (boundary >= 0) {
      const block = this.buffer.slice(0, boundary);
      this.buffer = this.buffer.slice(boundary + 2);
      let event = "message";
      const data: string[] = [];
      for (const line of block.split("\n")) {
        if (line.startsWith("event:")) event = line.slice(6).trim();
        if (line.startsWith("data:")) data.push(line.slice(5).trimStart());
      }
      if (data.length) emit(event, data.join("\n"));
      boundary = this.buffer.indexOf("\n\n");
    }
  }
}

export async function streamAsk(
  request: { question: string; doc: string; limit: number; history: Array<{ role: string; content: string }> },
  handlers: StreamHandlers,
  signal: AbortSignal,
): Promise<void> {
  const response = await fetch("/api/ask/stream", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(request),
    signal,
  });
  if (!response.ok || !response.body) {
    const payload = (await response.json().catch(() => ({}))) as { error?: string };
    throw new Error(payload.error || response.statusText || "Streaming request failed");
  }
  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  const parser = new SSEParser();
  let streamError: Error | undefined;
  let completed = false;
  const emit = (event: string, data: string) => {
    const payload = JSON.parse(data) as { content?: string; error?: string } | AskResult;
    if (event === "delta") handlers.onDelta((payload as { content?: string }).content || "");
    if (event === "result") {
      completed = true;
      handlers.onResult(payload as AskResult);
    }
    if (event === "error") streamError = new Error((payload as { error?: string }).error || "Streaming failed");
  };
  while (true) {
    const { value, done } = await reader.read();
    if (done) break;
    parser.push(decoder.decode(value, { stream: true }), emit);
    if (streamError) {
      await reader.cancel();
      throw streamError;
    }
  }
  parser.push(decoder.decode(), emit);
  if (streamError) throw streamError;
  if (!completed) throw new Error("Stream ended before a final result was received");
}
