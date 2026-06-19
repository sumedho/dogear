import type { Chat } from "./types";

export const storageKey = "dogear.chats.v1";

export function newChat(): Chat {
  const now = Date.now();
  return { id: crypto.randomUUID(), title: "New chat", documentId: "", draft: "", messages: [], createdAt: now, updatedAt: now };
}

export function loadChats(storage: Pick<Storage, "getItem"> = localStorage): Chat[] {
  try {
    const value = storage.getItem(storageKey);
    if (!value) return [];
    const parsed = JSON.parse(value) as Chat[];
    return Array.isArray(parsed) ? parsed
      .filter((chat) => chat && typeof chat.id === "string" && Array.isArray(chat.messages))
      .map((chat) => ({ ...chat, draft: typeof chat.draft === "string" ? chat.draft : "" })) : [];
  } catch {
    return [];
  }
}

export function saveChats(chats: Chat[], storage: Pick<Storage, "setItem"> = localStorage): void {
  storage.setItem(storageKey, JSON.stringify(chats));
}
