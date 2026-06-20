import type { Chat } from "./types";

export const storageKey = "dogear.chats.v1";
export const chatBackupVersion = 1;

export interface ChatBackup { version: number; exportedAt: string; chats: Chat[]; }

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
			.filter(validChat)
      .map((chat) => ({ ...chat, draft: typeof chat.draft === "string" ? chat.draft : "" })) : [];
  } catch {
    return [];
  }
}

export function saveChats(chats: Chat[], storage: Pick<Storage, "setItem"> = localStorage): void {
  storage.setItem(storageKey, JSON.stringify(chats));
}

function validChat(value: unknown): value is Chat {
  if (!value || typeof value !== "object") return false;
  const chat = value as Partial<Chat>;
  return typeof chat.id === "string" && typeof chat.title === "string" && typeof chat.documentId === "string" &&
    typeof chat.createdAt === "number" && typeof chat.updatedAt === "number" && Array.isArray(chat.messages) &&
    chat.messages.every((message) => message && (message.role === "user" || message.role === "assistant") && typeof message.id === "string" && typeof message.content === "string");
}

export function exportChats(chats: Chat[]): string {
  return JSON.stringify({ version: chatBackupVersion, exportedAt: new Date().toISOString(), chats }, null, 2);
}

export function mergeChatBackup(raw: string, current: Chat[]): { chats: Chat[]; added: number; duplicates: number } {
  let parsed: unknown;
  try { parsed = JSON.parse(raw); } catch { throw new Error("Backup is not valid JSON"); }
  if (!parsed || typeof parsed !== "object") throw new Error("Backup has an invalid structure");
  const backup = parsed as Partial<ChatBackup>;
  if (backup.version !== chatBackupVersion) throw new Error(`Unsupported chat backup version: ${String(backup.version)}`);
  if (!Array.isArray(backup.chats) || !backup.chats.every(validChat)) throw new Error("Backup contains invalid chat data");
  const ids = new Set(current.map((chat) => chat.id));
  const additions: Chat[] = [];
  for (const chat of backup.chats) {
    if (ids.has(chat.id)) continue;
    ids.add(chat.id);
    additions.push({ ...chat, draft: typeof chat.draft === "string" ? chat.draft : "" });
  }
  return { chats: [...additions, ...current], added: additions.length, duplicates: backup.chats.length - additions.length };
}
