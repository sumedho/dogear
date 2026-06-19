import type { AskResult, DocumentChunk, DocumentHealth, DocumentImportWarning, DocumentInfo, EmbeddingIndexStatus, Settings } from "./types";

async function json<T>(path: string, options?: RequestInit): Promise<T> {
  const response = await fetch(path, options);
  const payload = (await response.json()) as T & { error?: string };
  if (!response.ok) throw new Error(payload.error || response.statusText);
  return payload;
}

export function listDocuments(): Promise<DocumentInfo[]> {
  return json<DocumentInfo[]>("/api/documents");
}

export function removeDocument(documentId: string): Promise<{ ok: boolean }> {
  return json(`/api/documents/${encodeURIComponent(documentId)}`, { method: "DELETE" });
}

export function documentHealth(documentId: string): Promise<DocumentHealth> {
  return json(`/api/documents/${encodeURIComponent(documentId)}/health`);
}

export interface ImportMetadata { id: string; brand: string; model: string; tags: string; }

export async function importMarkdown(file: File, replace: boolean, metadata: ImportMetadata): Promise<{ documents: number; chunks: number; images: number; warnings?: DocumentImportWarning[] }> {
  const form = new FormData();
  form.set("file", file);
  form.set("replace", String(replace));
  form.set("id", metadata.id);
  form.set("brand", metadata.brand);
  form.set("model", metadata.model);
  form.set("tags", metadata.tags);
  return json("/api/import", { method: "POST", body: form });
}

export function listDocumentChunks(documentId: string, after = 0): Promise<DocumentChunk[]> {
  return json(`/api/documents/${encodeURIComponent(documentId)}/chunks?after=${after}&limit=50`);
}
export function getDocumentChunk(documentId: string, chunkId: number): Promise<DocumentChunk> {
  return json(`/api/documents/${encodeURIComponent(documentId)}/chunks/${chunkId}`);
}
export async function loadDocumentChunks(documentId: string, chunkId?: number): Promise<DocumentChunk[]> {
  const firstPagePromise = listDocumentChunks(documentId);
  if (!chunkId) return firstPagePromise;
  const [firstPage, target] = await Promise.all([firstPagePromise, getDocumentChunk(documentId, chunkId)]);
  if (firstPage.some((item) => item.id === target.id)) return firstPage;
  const targetPage = await listDocumentChunks(documentId, Math.max(0, target.ordinal - 1));
  if (targetPage.some((item) => item.id === target.id)) return targetPage;
  return [target, ...targetPage].sort((left, right) => left.ordinal - right.ordinal);
}
export async function getSettings(): Promise<Settings> {
  const settings = await json<Settings>("/api/settings");
  return { ...settings, environment_overrides: settings.environment_overrides ?? [] };
}
export function saveSettings(settings: Settings): Promise<Settings> { return json("/api/settings", { method: "PUT", headers: { "Content-Type": "application/json" }, body: JSON.stringify(settings) }); }
export function testSettings(target: "provider" | "embedding"): Promise<{ ok: boolean; model: string; dimensions?: number }> { return json("/api/settings/test", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ target }) }); }
export function embeddingIndexStatus(): Promise<EmbeddingIndexStatus> { return json("/api/index/embeddings/status"); }

export async function buildEmbeddingIndex(onProgress: (indexed: number, total: number) => void): Promise<EmbeddingIndexStatus> {
  const response = await fetch("/api/index/embeddings/stream", { method: "POST" });
  if (!response.ok || !response.body) throw new Error((await response.json().catch(() => ({})) as { error?: string }).error || response.statusText);
  const reader = response.body.getReader(); const decoder = new TextDecoder(); const parser = new SSEParser();
  let result: EmbeddingIndexStatus | undefined; let error: Error | undefined;
  const emit = (event: string, data: string) => { const value = JSON.parse(data) as EmbeddingIndexStatus & { error?: string; indexed: number; total: number }; if (event === "progress") onProgress(value.indexed, value.total); if (event === "result") result = value; if (event === "error") error = new Error(value.error || "Index build failed"); };
  while (true) { const next = await reader.read(); if (next.done) break; parser.push(decoder.decode(next.value, { stream: true }), emit); }
  if (error) throw error; if (!result) throw new Error("Index build ended without a result"); return result;
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
