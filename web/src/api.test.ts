import { describe, expect, it } from "vitest";
import { SSEParser } from "./api";

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
