import { afterEach, describe, expect, it, vi } from "vitest";
import { getSettings, SSEParser } from "./api";

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
