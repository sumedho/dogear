import { afterEach, describe, expect, it, vi } from "vitest";
import { getSettings, importMarkdown, loadDocumentChunks, removeDocument, SSEParser } from "./api";

afterEach(() => vi.unstubAllGlobals());

describe("getSettings", () => {
  it("normalizes null environment overrides from older servers", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({
      provider: {},
      embedding: {},
      environment_overrides: null,
    }), { status: 200 })));

    await expect(getSettings()).resolves.toMatchObject({ environment_overrides: [] });
  });
});

describe("importMarkdown", () => {
  it("sends document metadata with the upload", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ documents: 1, chunks: 2, images: 0 }), { status: 201 }));
    vi.stubGlobal("fetch", fetchMock);
    await importMarkdown(new File(["# Manual"], "manual.md", { type: "text/markdown" }), true, { id: "synth", brand: "Acme", model: "A1", tags: "studio, synth" });
    const body = fetchMock.mock.calls[0][1]?.body as FormData;
    expect(Object.fromEntries(body.entries())).toMatchObject({ id: "synth", brand: "Acme", model: "A1", tags: "studio, synth", replace: "true" });
  });
});

describe("removeDocument", () => {
  it("deletes the encoded document ID", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ ok: true }), { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);
    await removeDocument("manual/one");
    expect(fetchMock).toHaveBeenCalledWith("/api/documents/manual%2Fone", { method: "DELETE" });
  });
});

describe("loadDocumentChunks", () => {
  it("loads the target ordinal window when a deep-linked chunk is outside the first page", async () => {
    const chunk = (id: number, ordinal: number, heading: string) => ({
      id, document_id: "manual", ordinal, heading_path: heading, heading_level: 2,
      page_number: null, start_line: ordinal, end_line: ordinal, text: heading,
    });
    vi.stubGlobal("fetch", vi.fn(async (input: string | URL | Request) => {
      const url = String(input);
      if (url.includes("/chunks/900")) return new Response(JSON.stringify(chunk(900, 90, "10.8.5 PRB")));
      if (url.includes("after=89")) return new Response(JSON.stringify([
        chunk(900, 90, "10.8.5 PRB"),
        chunk(901, 91, "10.8.6 Next section"),
      ]));
      return new Response(JSON.stringify([chunk(1, 1, "FCC compliance statement")]));
    }));

    const chunks = await loadDocumentChunks("manual", 900);

    expect(chunks.map((item) => item.heading_path)).toEqual(["10.8.5 PRB", "10.8.6 Next section"]);
  });
});

describe("SSEParser", () => {
  it("parses events split across transport chunks", () => {
    const parser = new SSEParser();
    const events: Array<[string, string]> = [];
    const emit = (event: string, data: string) => events.push([event, data]);
    parser.push('event: delta\r\ndata: {"content":"hel', emit);
    parser.push('lo"}\r\n\r\nevent: result\ndata: {"answer":"hello"}\n\n', emit);
    expect(events).toEqual([
      ["delta", '{"content":"hello"}'],
      ["result", '{"answer":"hello"}'],
    ]);
  });

  it("joins multiple data lines", () => {
    const parser = new SSEParser();
    const events: string[] = [];
    parser.push("event: message\ndata: first\ndata: second\n\n", (_event, data) => events.push(data));
    expect(events).toEqual(["first\nsecond"]);
  });
});
