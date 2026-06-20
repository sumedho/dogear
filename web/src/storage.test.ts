import { describe, expect, it } from "vitest";
import { exportChats, loadChats, mergeChatBackup, saveChats, storageKey } from "./storage";
import type { Chat } from "./types";

describe("chat storage", () => {
  it("round trips chats", () => {
    const values = new Map<string, string>();
    const storage = {
      getItem: (key: string) => values.get(key) ?? null,
      setItem: (key: string, value: string) => void values.set(key, value),
    };
    const chats: Chat[] = [{ id: "one", title: "MIDI", documentId: "manual", draft: "clock setup", messages: [], createdAt: 1, updatedAt: 2 }];
    saveChats(chats, storage);
    expect(values.has(storageKey)).toBe(true);
    expect(loadChats(storage)).toEqual(chats);
  });

  it("adds an empty draft to legacy chats", () => {
    const legacy = [{ id: "one", title: "MIDI", documentId: "manual", messages: [], createdAt: 1, updatedAt: 2 }];
    expect(loadChats({ getItem: () => JSON.stringify(legacy) })[0].draft).toBe("");
  });

  it("marks unfinished responses as interrupted after reload", () => {
    const chats: Chat[] = [{ id: "one", title: "MIDI", documentId: "", draft: "", messages: [
      { id: "answer", role: "assistant", content: "Partial", status: "streaming" },
    ], createdAt: 1, updatedAt: 2 }];
    const loaded = loadChats({ getItem: () => JSON.stringify(chats) });
    expect(loaded[0].messages[0]).toMatchObject({ status: "cancelled", error: expect.stringContaining("interrupted") });
  });

  it("ignores malformed state", () => {
    expect(loadChats({ getItem: () => "not json" })).toEqual([]);
    expect(loadChats({ getItem: () => JSON.stringify([{ nope: true }]) })).toEqual([]);
  });

  it("exports and merges versioned backups without duplicating IDs", () => {
    const existing: Chat[] = [{ id: "one", title: "Existing", documentId: "", draft: "", messages: [], createdAt: 1, updatedAt: 1 }];
    const imported: Chat[] = [existing[0], { id: "two", title: "Imported", documentId: "manual", draft: "", messages: [], createdAt: 2, updatedAt: 2 }];
    expect(mergeChatBackup(exportChats(imported), existing)).toMatchObject({ added: 1, duplicates: 1, chats: [{ id: "two" }, { id: "one" }] });
  });

  it("rejects malformed and unsupported backups", () => {
    expect(() => mergeChatBackup("not json", [])).toThrow("valid JSON");
    expect(() => mergeChatBackup(JSON.stringify({ version: 99, chats: [] }), [])).toThrow("Unsupported");
    expect(() => mergeChatBackup(JSON.stringify({ version: 1, chats: [{ nope: true }] }), [])).toThrow("invalid chat data");
  });
});
