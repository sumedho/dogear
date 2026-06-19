import { describe, expect, it } from "vitest";
import { loadChats, saveChats, storageKey } from "./storage";
import type { Chat } from "./types";

describe("chat storage", () => {
  it("round trips chats", () => {
    const values = new Map<string, string>();
    const storage = {
      getItem: (key: string) => values.get(key) ?? null,
      setItem: (key: string, value: string) => void values.set(key, value),
    };
    const chats: Chat[] = [{ id: "one", title: "MIDI", documentId: "manual", messages: [], createdAt: 1, updatedAt: 2 }];
    saveChats(chats, storage);
    expect(values.has(storageKey)).toBe(true);
    expect(loadChats(storage)).toEqual(chats);
  });

  it("ignores malformed state", () => {
    expect(loadChats({ getItem: () => "not json" })).toEqual([]);
    expect(loadChats({ getItem: () => JSON.stringify([{ nope: true }]) })).toEqual([]);
  });
});
