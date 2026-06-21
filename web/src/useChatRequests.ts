import { useRef, useState } from "react";

export function useChatRequests() {
  const controllers = useRef(new Map<string, AbortController>());
  const [activeChats, setActiveChats] = useState<Set<string>>(() => new Set());

  const begin = (chatId: string): AbortController | null => {
    if (controllers.current.has(chatId)) return null;
    const controller = new AbortController();
    controllers.current.set(chatId, controller);
    setActiveChats((items) => new Set(items).add(chatId));
    return controller;
  };

  const finish = (chatId: string, controller: AbortController) => {
    if (controllers.current.get(chatId) !== controller) return;
    controllers.current.delete(chatId);
    setActiveChats((items) => { const next = new Set(items); next.delete(chatId); return next; });
  };

  const stop = (chatId: string) => controllers.current.get(chatId)?.abort();
  const controllerFor = (chatId: string) => controllers.current.get(chatId);
  return { activeChats, begin, finish, stop, controllerFor };
}
