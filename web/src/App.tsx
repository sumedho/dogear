import { useEffect, useMemo, useRef, useState } from "react";
import ReactMarkdown, { defaultUrlTransform } from "react-markdown";
import remarkGfm from "remark-gfm";
import { buildEmbeddingIndex, documentHealth, embeddingIndexStatus, getSettings, importMarkdown, listDocumentChunks, listDocuments, loadDocumentChunks, removeDocument as removeDocumentAPI, saveSettings, searchManual, streamAsk, testSettings } from "./api";
import { exportChats, loadChats, mergeChatBackup, newChat, saveChats } from "./storage";
import type { AskResult, Chat, ChatMessage, DisplayImage, DocumentChunk, DocumentHealth, DocumentInfo, EmbeddingIndexStatus, SearchResult, Settings, SourceRef } from "./types";

function useDialog<T extends HTMLElement>(onClose: () => void, canClose = true) {
  const ref = useRef<T>(null);
  const onCloseRef = useRef(onClose);
  onCloseRef.current = onClose;
  useEffect(() => {
    const previous = document.activeElement as HTMLElement | null;
    const dialog = ref.current;
    const focusable = () => Array.from(dialog?.querySelectorAll<HTMLElement>('button:not(:disabled), input:not(:disabled), select:not(:disabled), textarea:not(:disabled), [href], [tabindex]:not([tabindex="-1"])') || []);
    requestAnimationFrame(() => focusable()[0]?.focus());
    const keydown = (event: KeyboardEvent) => {
      if (event.key === "Escape" && canClose) { event.preventDefault(); onCloseRef.current(); return; }
      if (event.key !== "Tab") return;
      const items = focusable();
      if (!items.length) return;
      const first = items[0]; const last = items[items.length - 1];
      if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus(); }
      else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); }
    };
    window.addEventListener("keydown", keydown);
    return () => { window.removeEventListener("keydown", keydown); previous?.focus(); };
  }, [canClose]);
  return ref;
}

type Toast = { message: string; action?: { label: string; run(): void } };

function previousUserIndex(messages: ChatMessage[], before: number): number {
  for (let index = before - 1; index >= 0; index--) if (messages[index].role === "user") return index;
  return -1;
}

function chatGroup(updatedAt: number): string {
  const age = Date.now() - updatedAt;
  if (age < 24 * 60 * 60 * 1000) return "Today";
  if (age < 7 * 24 * 60 * 60 * 1000) return "Previous 7 days";
  return "Older";
}

function sourceDescription(source: SourceRef): string {
  const parts = [source.title];
  if (source.page_number) parts.push(`p.${source.page_number}`);
  if (source.heading_path) parts.push(source.heading_path);
  parts.push(`lines ${source.start_line}–${source.end_line}`);
  return parts.join(" · ");
}

type MarkdownNode = { type: string; value?: string; url?: string; children?: MarkdownNode[] };

function remarkCitations() {
  return (tree: MarkdownNode) => {
    const visit = (node: MarkdownNode) => {
      if (!node.children || node.type === "link") return;
      for (let index = 0; index < node.children.length; index++) {
        const child = node.children[index];
        if (child.type !== "text" || !child.value) {
          visit(child);
          continue;
        }
        const parts: MarkdownNode[] = [];
        let offset = 0;
        for (const match of child.value.matchAll(/\[(\d+)\]/g)) {
          if (match.index > offset) parts.push({ type: "text", value: child.value.slice(offset, match.index) });
          parts.push({ type: "link", url: `citation:${match[1]}`, children: [{ type: "text", value: match[0] }] });
          offset = match.index + match[0].length;
        }
        if (!parts.length) continue;
        if (offset < child.value.length) parts.push({ type: "text", value: child.value.slice(offset) });
        node.children.splice(index, 1, ...parts);
        index += parts.length - 1;
      }
    };
    visit(tree);
  };
}

function AnswerMarkdown({ message, onOpen }: { message: ChatMessage; onOpen(documentId: string, chunkId?: number): void }) {
  return <ReactMarkdown
    remarkPlugins={[remarkGfm, remarkCitations]}
    urlTransform={(url) => url.startsWith("citation:") ? url : defaultUrlTransform(url)}
    components={{ a: ({ href, children }) => {
      const label = href?.startsWith("citation:") ? `[${href.slice("citation:".length)}]` : "";
      const source = message.sources?.find((candidate) => candidate.label === label);
      if (source) return <button type="button" className="citation" title={sourceDescription(source)} onClick={() => onOpen(source.document_id, source.chunk_id)}>{children}</button>;
      if (label) return <span className="citation citation-pending">{children}</span>;
      return <a href={href}>{children}</a>;
    } }}
  >{message.content || (message.status === "streaming" ? "Searching the manual…" : "")}</ReactMarkdown>;
}

function initialChats(): Chat[] {
  const saved = loadChats();
  return saved.length ? saved : [newChat()];
}

export default function App() {
  const [chats, setChats] = useState<Chat[]>(initialChats);
  const [activeId, setActiveId] = useState(() => chats[0].id);
  const [documents, setDocuments] = useState<DocumentInfo[]>([]);
  const [editingChat, setEditingChat] = useState<{ id: string; title: string } | null>(null);
  const [deleteChat, setDeleteChat] = useState<Chat | null>(null);
  const [healthDocument, setHealthDocument] = useState<DocumentInfo | null>(null);
  const [deleteDocument, setDeleteDocument] = useState<DocumentInfo | null>(null);
  const [browseOpen, setBrowseOpen] = useState(false);
  const [importOpen, setImportOpen] = useState(false);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [viewer, setViewer] = useState<{ documentId: string; chunkId?: number } | null>(null);
  const [sidebarOpen, setSidebarOpen] = useState(false);
  const [loadingDocs, setLoadingDocs] = useState(true);
  const [docsError, setDocsError] = useState("");
  const [controller, setController] = useState<AbortController | null>(null);
  const [toast, setToast] = useState<Toast | null>(null);
  const [chatFilter, setChatFilter] = useState("");
  const [readiness, setReadiness] = useState<{ label: string; state: "ready" | "limited" | "needed"; detail: string }>({ label: "Checking…", state: "limited", detail: "Checking configuration" });
  const [pendingScope, setPendingScope] = useState<string | null>(null);
  const messagesRef = useRef<HTMLElement>(null);
  const composerRef = useRef<HTMLTextAreaElement>(null);
  const chatFilterRef = useRef<HTMLInputElement>(null);
  const chatImportRef = useRef<HTMLInputElement>(null);
  const cancelRenameRef = useRef(false);
  const current = chats.find((chat) => chat.id === activeId) || chats[0];

  const refreshDocuments = async () => {
    setLoadingDocs(true);
    try {
      setDocuments(await listDocuments());
      setDocsError("");
    } catch (error) {
      setDocsError(error instanceof Error ? error.message : "Could not load manuals");
    } finally {
      setLoadingDocs(false);
    }
  };

  const refreshReadiness = async () => {
    try {
      const [settings, index, library] = await Promise.all([getSettings(), embeddingIndexStatus(), listDocuments()]);
      if (!library.length) setReadiness({ label: "Setup needed", state: "needed", detail: "Import a manual" });
      else if (!settings.provider.model.trim() && !settings.environment_overrides.includes("DOGEAR_MODEL")) setReadiness({ label: "Setup needed", state: "needed", detail: "Configure a chat model" });
      else if (!index.configured) setReadiness({ label: "FTS only", state: "limited", detail: "Embeddings are not configured" });
      else if (index.stale || !index.complete) setReadiness({ label: "Index stale", state: "limited", detail: "Rebuild the embedding index" });
      else setReadiness({ label: "Hybrid ready", state: "ready", detail: `${index.indexed}/${index.total} chunks indexed` });
    } catch (error) {
      setReadiness({ label: "Setup needed", state: "needed", detail: error instanceof Error ? error.message : "Could not read configuration" });
    }
  };

  useEffect(() => { void refreshDocuments(); void refreshReadiness(); }, []);
	useEffect(() => {
		const restore = () => { const match = location.hash.match(/^#manual\/([^/]+)(?:\/chunk\/(\d+))?/); setViewer(match ? { documentId: decodeURIComponent(match[1]), chunkId: match[2] ? Number(match[2]) : undefined } : null); };
		restore(); window.addEventListener("hashchange", restore); return () => window.removeEventListener("hashchange", restore);
	}, []);
  useEffect(() => { try { saveChats(chats); } catch (error) { setToast({ message: error instanceof Error ? `Chats could not be saved: ${error.message}` : "Chats could not be saved" }); } }, [chats]);
  useEffect(() => {
    const messages = messagesRef.current;
    if (messages) messages.scrollTo({ top: messages.scrollHeight, behavior: "smooth" });
    if (window.scrollY) window.scrollTo(0, 0);
  }, [current?.messages]);
  useEffect(() => { if (!toast) return; const timer = window.setTimeout(() => setToast(null), 5000); return () => clearTimeout(timer); }, [toast]);

  useEffect(() => {
    const keydown = (event: KeyboardEvent) => {
      const target = event.target as HTMLElement | null;
      const editing = target?.matches("input, textarea, select, [contenteditable='true']");
      const blockingOverlay = settingsOpen || importOpen || browseOpen || !!deleteChat || !!deleteDocument || !!healthDocument || pendingScope !== null;
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "n" && !blockingOverlay && !viewer) { event.preventDefault(); createChat(); }
      else if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "k" && !blockingOverlay) { event.preventDefault(); (viewer ? document.querySelector<HTMLInputElement>(".viewer-search input") : chatFilterRef.current)?.focus(); }
      else if (event.key === "/" && !editing && !settingsOpen && !importOpen && !browseOpen && !viewer) { event.preventDefault(); composerRef.current?.focus(); }
      else if (event.key === "Escape" && sidebarOpen) setSidebarOpen(false);
    };
    window.addEventListener("keydown", keydown);
    return () => window.removeEventListener("keydown", keydown);
  });

  const updateChat = (chatId: string, update: (chat: Chat) => Chat) => {
    setChats((items) => items.map((chat) => (chat.id === chatId ? update(chat) : chat)));
  };

  const createChat = () => {
    const chat = newChat();
    setChats((items) => [chat, ...items]);
    setActiveId(chat.id);
    setPendingScope(null);
    setSidebarOpen(false);
  };

  const removeChat = (id: string) => {
    const remaining = chats.filter((chat) => chat.id !== id);
    if (!remaining.length) {
      const replacement = newChat();
      setChats([replacement]);
      setActiveId(replacement.id);
      return;
    }
    setChats(remaining);
    if (activeId === id) setActiveId(remaining[0].id);
  };

  const selectDocument = (documentId: string) => {
    if (documentId !== current.documentId && current.messages.length > 0) { setPendingScope(documentId); return; }
    updateChat(current.id, (chat) => ({ ...chat, documentId, updatedAt: Date.now() }));
  };

  const applyDocument = (documentId: string) => {
    updateChat(current.id, (chat) => ({ ...chat, documentId, updatedAt: Date.now() }));
    setPendingScope(null);
  };

  const renameChat = () => {
    if (!editingChat?.title.trim()) { setEditingChat(null); return; }
    updateChat(editingChat.id, (chat) => ({ ...chat, title: editingChat.title.trim(), updatedAt: Date.now() }));
    setEditingChat(null);
  };

  const runAnswer = async (chat: Chat, question: string, history: Array<{ role: string; content: string }>, assistantId: string) => {
    if (controller) return;
    const abort = new AbortController();
    setController(abort);
    try {
      await streamAsk(
        { question, doc: chat.documentId, limit: 8, history },
        {
          onDelta: (content) => updateChat(chat.id, (value) => ({ ...value, messages: value.messages.map((message) => message.id === assistantId ? { ...message, content: message.content + content } : message), updatedAt: Date.now() })),
          onResult: (result: AskResult) => updateChat(chat.id, (value) => ({ ...value, messages: value.messages.map((message) => message.id === assistantId ? { ...message, content: result.answer, status: "done", sources: result.sources, retrieval: result.retrieval, images: result.images, error: undefined } : message), updatedAt: Date.now() })),
        },
        abort.signal,
      );
    } catch (error) {
      const cancelled = error instanceof DOMException && error.name === "AbortError";
      updateChat(chat.id, (value) => ({ ...value, messages: value.messages.map((message) => message.id === assistantId ? { ...message, status: cancelled ? "cancelled" : "error", error: cancelled ? "Stopped" : error instanceof Error ? error.message : "Request failed" } : message) }));
    } finally {
      setController(null);
    }
  };

  const send = async () => {
    const question = current.draft.trim();
    if (!question || controller) return;
    const chat = current;
    const userMessage: ChatMessage = { id: crypto.randomUUID(), role: "user", content: question, status: "done" };
    const assistantId = crypto.randomUUID();
    const assistantMessage: ChatMessage = { id: assistantId, role: "assistant", content: "", status: "streaming" };
    const history = chat.messages
      .filter((message) => message.content && message.status !== "error" && message.status !== "cancelled")
      .map(({ role, content }) => ({ role, content }));
    updateChat(chat.id, (value) => ({
      ...value,
      title: value.messages.length ? value.title : question.slice(0, 48),
      draft: "",
      messages: [...value.messages, userMessage, assistantMessage],
      updatedAt: Date.now(),
    }));
    await runAnswer(chat, question, history, assistantId);
  };

  const retryAnswer = async (assistantId: string) => {
    if (controller) return;
    const assistantIndex = current.messages.findIndex((message) => message.id === assistantId);
    const userIndex = previousUserIndex(current.messages, assistantIndex);
    if (userIndex < 0) return;
    const question = current.messages[userIndex].content;
    const history = current.messages.slice(0, userIndex).filter((message) => message.content && message.status !== "error" && message.status !== "cancelled").map(({ role, content }) => ({ role, content }));
    updateChat(current.id, (chat) => ({ ...chat, messages: chat.messages.map((message) => message.id === assistantId ? { ...message, content: "", status: "streaming", error: undefined, sources: undefined, retrieval: undefined, images: undefined } : message), updatedAt: Date.now() }));
    await runAnswer(current, question, history, assistantId);
  };

  const editQuestion = (assistantId: string) => {
    const index = current.messages.findIndex((message) => message.id === assistantId);
    const userIndex = previousUserIndex(current.messages, index);
    if (userIndex < 0) return;
    updateChat(current.id, (chat) => ({ ...chat, draft: current.messages[userIndex].content, updatedAt: Date.now() }));
    requestAnimationFrame(() => composerRef.current?.focus());
  };

  const activeDocument = documents.find((document) => document.id === current.documentId);
	const openViewer = (documentId: string, chunkId?: number) => { location.hash = `manual/${encodeURIComponent(documentId)}${chunkId ? `/chunk/${chunkId}` : ""}`; };
  const closeViewer = () => { history.pushState(null, "", location.pathname + location.search); setViewer(null); };
  const askAboutSection = (chunk: DocumentChunk) => {
    const prefix = `In the “${chunk.heading_path || `Chunk ${chunk.ordinal}`}” section, `;
    updateChat(current.id, (chat) => ({ ...chat, documentId: chunk.document_id, draft: prefix + chat.draft, updatedAt: Date.now() }));
    closeViewer();
    requestAnimationFrame(() => composerRef.current?.focus());
  };
  const removeManual = async (document: DocumentInfo) => {
    await removeDocumentAPI(document.id);
    setDocuments((items) => items.filter((item) => item.id !== document.id));
    setChats((items) => items.map((chat) => chat.documentId === document.id ? { ...chat, documentId: "", updatedAt: Date.now() } : chat));
    if (viewer?.documentId === document.id) closeViewer();
    setDeleteDocument(null);
    setToast({ message: `Removed ${document.title}` });
    void refreshReadiness();
  };

  const deleteChatWithUndo = (chat: Chat) => {
    const previousChats = chats;
    const previousActiveId = activeId;
    removeChat(chat.id);
    setToast({ message: `Deleted “${chat.title}”`, action: { label: "Undo", run: () => { setChats(previousChats); setActiveId(previousActiveId); setToast(null); } } });
  };

  const downloadChats = () => {
    const url = URL.createObjectURL(new Blob([exportChats(chats)], { type: "application/json" }));
    const link = document.createElement("a");
    link.href = url; link.download = `dogear-chats-${new Date().toISOString().slice(0, 10)}.json`; link.click();
    window.setTimeout(() => URL.revokeObjectURL(url), 1000);
    setToast({ message: `Exported ${chats.length} chat${chats.length === 1 ? "" : "s"}` });
  };

  const importChatBackup = async (file?: File) => {
    if (!file) return;
    try {
      const result = mergeChatBackup(await file.text(), chats);
      setChats(result.chats);
      setToast({ message: `Imported ${result.added} chat${result.added === 1 ? "" : "s"}${result.duplicates ? ` · ${result.duplicates} duplicate${result.duplicates === 1 ? "" : "s"} skipped` : ""}` });
    } catch (error) {
      setToast({ message: error instanceof Error ? error.message : "Could not import chat backup" });
    } finally {
      if (chatImportRef.current) chatImportRef.current.value = "";
    }
  };

  const normalizedChatFilter = chatFilter.trim().toLowerCase();
  const visibleChats = chats.filter((chat) => !normalizedChatFilter || chat.title.toLowerCase().includes(normalizedChatFilter) || chat.messages.some((message) => message.content.toLowerCase().includes(normalizedChatFilter)));
  const groupedChats = ["Today", "Previous 7 days", "Older"].map((label) => ({ label, chats: visibleChats.filter((chat) => chatGroup(chat.updatedAt) === label) })).filter((group) => group.chats.length);

  const suggestedPrompts = activeDocument
    ? [`How do I get started with the ${activeDocument.model || activeDocument.title}?`, "What are the most important settings?", "Show me the MIDI configuration steps"]
    : ["Compare the setup instructions across these manuals", "Where is MIDI sync configured?", "Summarize the main troubleshooting steps"];

  return (
    <div className="app-shell">
      <aside className={`sidebar ${sidebarOpen ? "sidebar-open" : ""}`}>
        <div className="brand-row">
          <div className="brand-mark">D</div>
          <strong>DogEar</strong>
          <button className="icon-button mobile-only" onClick={() => setSidebarOpen(false)} aria-label="Close sidebar">×</button>
        </div>
        <button className="new-chat" onClick={createChat} title="New chat (⌘/Ctrl+N)"><span>＋</span> New chat</button>
        <div className="library-actions">
          <button onClick={() => setBrowseOpen(true)}>▤ <span>Browse manuals</span></button>
          <button onClick={() => setImportOpen(true)}>⇧ <span>Import Markdown</span></button>
			<button onClick={() => setSettingsOpen(true)}>⚙ <span>Settings</span></button>
        </div>
        <label className="chat-filter" title="Search chats (⌘/Ctrl+K)"><span className="sr-only">Search chats</span><input ref={chatFilterRef} type="search" value={chatFilter} onChange={(event) => setChatFilter(event.target.value)} placeholder="Search chats" /></label>
        <nav className="chat-list">
          {groupedChats.map((group) => <section className="chat-group" key={group.label}><div className="sidebar-label">{group.label}</div>{group.chats.map((chat) => (
            <div className={`chat-row ${chat.id === current.id ? "active" : ""}`} key={chat.id}>
              {editingChat?.id === chat.id
                ? <input className="chat-rename" autoFocus value={editingChat.title} onChange={(event) => setEditingChat({ id: chat.id, title: event.target.value })} onBlur={() => { if (!cancelRenameRef.current) renameChat(); cancelRenameRef.current = false; }} onKeyDown={(event) => { if (event.key === "Enter") renameChat(); if (event.key === "Escape") { cancelRenameRef.current = true; setEditingChat(null); } }} aria-label="Chat title" />
                : <button className="chat-select" onClick={() => { setActiveId(chat.id); setPendingScope(null); setSidebarOpen(false); }}>{chat.title}</button>}
              <button className="chat-action" onClick={() => { cancelRenameRef.current = false; setEditingChat({ id: chat.id, title: chat.title }); }} aria-label={`Rename ${chat.title}`}>✎</button>
              <button className="chat-action delete-chat" onClick={() => setDeleteChat(chat)} aria-label={`Delete ${chat.title}`}>×</button>
            </div>
          ))}</section>)}
          {!visibleChats.length && <div className="sidebar-empty">No matching chats</div>}
        </nav>
        <div className="sidebar-footer"><button onClick={downloadChats}>Export chats</button><button onClick={() => chatImportRef.current?.click()}>Import chats</button><input ref={chatImportRef} type="file" accept="application/json,.json" onChange={(event) => void importChatBackup(event.target.files?.[0])} /><span>Local manual assistant</span></div>
      </aside>

      <main className="chat-main">
        <header className="topbar">
          <button className="sidebar-toggle" onClick={() => setSidebarOpen(true)} aria-label="Open sidebar"><span aria-hidden="true">☰</span> Menu</button>
          <select value={current.documentId} onChange={(event) => selectDocument(event.target.value)} aria-label="Manual for this chat">
            <option value="">All manuals</option>
            {documents.map((document) => <option key={document.id} value={document.id}>{document.title}</option>)}
          </select>
          <span className="topbar-status">{activeDocument ? `${activeDocument.chunk_count} indexed chunks` : documents.length ? `${documents.length} manuals` : "No manuals"}</span>
          <button className={`readiness readiness-${readiness.state}`} title={readiness.detail} onClick={() => setSettingsOpen(true)}>{readiness.label}</button>
        </header>

	        <section ref={messagesRef} className="messages">
          {!current.messages.length && (
            <div className="empty-state">
              <div className="empty-logo">D</div>
              <h1>Ask your manuals</h1>
              <p>{documents.length ? "Choose a manual or search across all of them, then ask a question." : "Import a Markdown manual, check your provider, and ask your first question."}</p>
              {!documents.length ? <div className="onboarding-steps"><button onClick={() => setImportOpen(true)}>1. Import Markdown</button><button className="secondary-button" onClick={() => setSettingsOpen(true)}>2. Configure provider</button></div>
                : <div className="suggested-prompts">{suggestedPrompts.map((prompt) => <button key={prompt} onClick={() => { updateChat(current.id, (chat) => ({ ...chat, draft: prompt })); requestAnimationFrame(() => composerRef.current?.focus()); }}>{prompt}</button>)}</div>}
            </div>
          )}
          {current.messages.map((message) => (
            <article className={`message message-${message.role}`} key={message.id}>
              <div className="avatar">{message.role === "user" ? "Y" : "D"}</div>
              <div className="message-body">
                <div className="message-author">{message.role === "user" ? "You" : "DogEar"}</div>
                {message.role === "assistant" ? (
                  <div className="markdown"><AnswerMarkdown message={message} onOpen={openViewer} /></div>
                ) : <div className="user-text">{message.content}</div>}
				{message.images && message.images.length > 0 && <AnswerImages images={message.images} onOpen={openViewer} />}
                {message.status === "streaming" && <span className="streaming-cursor" aria-label="Streaming" />}
                {message.error && <div className={`message-error ${message.status === "cancelled" ? "muted" : ""}`}>{message.error}</div>}
                {message.role === "assistant" && message.retrieval?.fallback_reason && <div className="retrieval-note"><span>Used full-text search: {message.retrieval.fallback_reason}.</span><button onClick={() => setSettingsOpen(true)}>{message.retrieval.fallback_reason.includes("index") ? "Rebuild index" : "Configure embeddings"}</button></div>}
                {message.sources && message.sources.length > 0 && <SourceCards message={message} onOpen={openViewer} />}
                {message.role === "assistant" && message.status !== "streaming" && <div className="message-actions">
                  <button onClick={() => void navigator.clipboard.writeText(message.content)} disabled={!message.content}>Copy answer</button>
                  <button onClick={() => void retryAnswer(message.id)} disabled={!!controller}>{message.status === "done" ? "Regenerate" : "Retry"}</button>
                  <button onClick={() => editQuestion(message.id)}>Edit question</button>
                  {message.status === "error" && <button onClick={() => setSettingsOpen(true)}>Open settings</button>}
                </div>}
              </div>
            </article>
          ))}
          <div className="sr-only" role="status" aria-live="polite">{controller ? "DogEar is generating a response" : ""}</div>
        </section>

        <div className="composer-wrap">
          <div className="composer">
            <textarea
              ref={composerRef}
              value={current.draft}
              onChange={(event) => updateChat(current.id, (chat) => ({ ...chat, draft: event.target.value, updatedAt: Date.now() }))}
              onKeyDown={(event) => {
                if (event.key === "Enter" && !event.shiftKey) {
                  event.preventDefault();
                  void send();
                }
              }}
              placeholder={documents.length ? "Message DogEar" : "Import a manual before asking"}
              disabled={!documents.length}
              title="Press / to focus · Enter to send · Shift+Enter for a new line"
              rows={1}
            />
            {controller ? <button className="send-button stop" onClick={() => controller.abort()} aria-label="Stop response">■</button>
              : <button className="send-button" onClick={() => void send()} disabled={!current.draft.trim() || !documents.length} aria-label="Send message">↑</button>}
          </div>
          <div className="composer-note">Answers are grounded in retrieved manual sections. Verify important details against the cited sources.</div>
        </div>
      </main>

      {browseOpen && <ManualDialog documents={documents} loading={loadingDocs} error={docsError} onRefresh={refreshDocuments} onSelect={(id) => { selectDocument(id); setBrowseOpen(false); }} onOpen={(id) => { setBrowseOpen(false); openViewer(id); }} onHealth={(document) => { setBrowseOpen(false); setHealthDocument(document); }} onRemove={(document) => { setBrowseOpen(false); setDeleteDocument(document); }} onClose={() => setBrowseOpen(false)} />}
      {importOpen && <ImportDialog onClose={() => setImportOpen(false)} onImported={async () => { await refreshDocuments(); await refreshReadiness(); setToast({ message: "Manual library updated" }); }} />}
			{settingsOpen && <SettingsDialog onClose={() => setSettingsOpen(false)} onNotify={(message) => setToast({ message })} onChanged={() => void refreshReadiness()} />}
		{viewer && <ManualViewer manual={documents.find((item) => item.id === viewer.documentId)} documentId={viewer.documentId} chunkId={viewer.chunkId} onAsk={askAboutSection} onClose={closeViewer} />}
      {deleteChat && <ConfirmDeleteChat chat={deleteChat} streaming={deleteChat.messages.some((message) => message.status === "streaming")} onCancel={() => setDeleteChat(null)} onConfirm={() => { if (deleteChat.messages.some((message) => message.status === "streaming")) controller?.abort(); deleteChatWithUndo(deleteChat); setDeleteChat(null); }} />}
      {healthDocument && <DocumentHealthDialog document={healthDocument} onClose={() => setHealthDocument(null)} onSettings={() => { setHealthDocument(null); setSettingsOpen(true); }} />}
      {deleteDocument && <ConfirmDeleteDocument document={deleteDocument} onCancel={() => setDeleteDocument(null)} onConfirm={() => removeManual(deleteDocument)} />}
      {pendingScope !== null && <ConfirmScopeChange current={activeDocument?.title || "All manuals"} next={documents.find((document) => document.id === pendingScope)?.title || "All manuals"} onCancel={() => setPendingScope(null)} onConfirm={() => applyDocument(pendingScope)} />}
      {sidebarOpen && <button className="sidebar-scrim" onClick={() => setSidebarOpen(false)} aria-label="Close sidebar" />}
      {toast && <div className="toast" role="status"><span>{toast.message}</span>{toast.action && <button onClick={toast.action.run}>{toast.action.label}</button>}<button className="toast-close" onClick={() => setToast(null)} aria-label="Dismiss notification">×</button></div>}
    </div>
  );
}

function AnswerImages({ images, onOpen }: { images: DisplayImage[]; onOpen(documentId: string, chunkId?: number): void }) {
  return <div className="answer-images" aria-label="Retrieved images">
    {images.map((image) => <figure key={image.id}>
      <button onClick={() => onOpen(image.source.document_id, image.source.chunk_id)} aria-label={`Open ${image.alt} in manual`}>
        <img src={`/api/images/${image.id}`} alt={image.alt} loading="lazy" />
      </button>
      <figcaption>{image.alt} <span>{image.source.label}</span></figcaption>
    </figure>)}
  </div>;
}

function SourceCards({ message, onOpen }: { message: ChatMessage; onOpen(documentId: string, chunkId?: number): void }) {
  const blocks = message.retrieval?.blocks || [];
  return (
    <details className="sources">
      <summary>{message.sources?.length} sources</summary>
      <div className="source-grid">
        {message.sources?.map((source) => {
          const block = blocks.find((item) => item.source.label === source.label);
          return (
            <div className="source-card" key={`${source.document_id}-${source.label}`}>
              <button className="source-open" onClick={() => onOpen(source.document_id, source.chunk_id)}>
                <div className="source-title"><span>{source.label}</span> {sourceDescription(source)}</div>
                {block?.text && <p>{block.text}</p>}
                {block?.images?.map((image) => <img key={image.id} src={`/api/images/${image.id}`} alt={image.alt} loading="lazy" />)}
              </button>
              <button className="copy-citation" onClick={() => void navigator.clipboard.writeText(`${source.label} ${sourceDescription(source)}`)}>Copy citation</button>
            </div>
          );
        })}
      </div>
    </details>
  );
}

function ManualDialog({ documents, loading, error, onRefresh, onSelect, onOpen, onHealth, onRemove, onClose }: {
  documents: DocumentInfo[]; loading: boolean; error: string; onRefresh(): Promise<void>; onSelect(id: string): void; onOpen(id: string): void; onHealth(document: DocumentInfo): void; onRemove(document: DocumentInfo): void; onClose(): void;
}) {
  const dialogRef = useDialog<HTMLDivElement>(onClose);
  const [filter, setFilter] = useState("");
  const normalized = filter.trim().toLowerCase();
  const visible = documents.filter((document) => !normalized || [document.title, document.brand, document.model, document.id, ...document.tags].join(" ").toLowerCase().includes(normalized));
  return <div className="modal-backdrop" role="presentation"><div ref={dialogRef} className="modal" role="dialog" aria-modal="true" aria-label="Manual library">
	    <div className="modal-header"><div><h2>Manual library</h2><p>Select the manual used by the current chat.</p></div><button className="icon-button" onClick={onClose} aria-label="Close manual library">×</button></div>
    <label className="library-filter"><span className="sr-only">Filter manuals</span><input value={filter} onChange={(event) => setFilter(event.target.value)} placeholder="Filter by title, brand, model, or tag" /></label>
    {loading && <div className="modal-status">Loading manuals…</div>}
    {error && <div className="message-error">{error} <button onClick={() => void onRefresh()}>Retry</button></div>}
    {!loading && !error && <div className="manual-list">
      <button className="manual-item" onClick={() => onSelect("")}><strong>All manuals</strong><span>Search every imported document</span></button>
      {visible.map((document) => <div className="manual-item" key={document.id}>
		<strong>{document.title}</strong><div className="manual-metadata"><span><b>ID</b> {document.id}</span>{document.brand && <span><b>Brand</b> {document.brand}</span>}{document.model && <span><b>Model</b> {document.model}</span>}{document.tags.length > 0 && <span><b>Tags</b> {document.tags.join(", ")}</span>}</div><span>{document.chunk_count} chunks · {document.page_count} pages</span>
		<div className="manual-actions"><button onClick={() => onSelect(document.id)}>Use in chat</button><button onClick={() => onOpen(document.id)}>Open manual</button><button onClick={() => onHealth(document)}>Health</button><button className="remove-manual" onClick={() => onRemove(document)}>Remove</button></div>
      </div>)}
      {normalized && !visible.length && <div className="modal-status">No manuals match “{filter}”.</div>}
    </div>}
  </div></div>;
}

function ManualViewer({ manual, documentId, chunkId, onAsk, onClose }: { manual?: DocumentInfo; documentId: string; chunkId?: number; onAsk(chunk: DocumentChunk): void; onClose(): void }) {
  const [chunks, setChunks] = useState<DocumentChunk[]>([]);
  const [loading, setLoading] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const [hasMore, setHasMore] = useState(true);
  const [error, setError] = useState("");
  const [query, setQuery] = useState("");
  const [results, setResults] = useState<SearchResult[]>([]);
  const [searching, setSearching] = useState(false);
  const [tocOpen, setTocOpen] = useState(false);
  const loadingMoreRef = useRef(false);
  const dialogRef = useDialog<HTMLElement>(onClose);
  useEffect(() => {
    let active = true; setLoading(true); setHasMore(true); setError(""); loadingMoreRef.current = false;
    loadDocumentChunks(documentId, chunkId).then((page) => {
      if (!active) return; setChunks(page); setLoading(false); setHasMore(page.length === 50);
      setTimeout(() => {
        const target = document.getElementById(`chunk-${chunkId}`);
        const container = target?.closest("main");
        if (target && container) container.scrollTo({ top: target.offsetTop - 20, behavior: "smooth" });
      }, 50);
    }).catch((reason) => { if (active) { setError(reason instanceof Error ? reason.message : "Could not load manual"); setLoading(false); } });
    return () => { active = false; };
  }, [documentId, chunkId]);
  useEffect(() => {
    const value = query.trim();
    if (!value) { setResults([]); setSearching(false); return; }
    let active = true; setSearching(true);
    const timer = window.setTimeout(() => searchManual(value, documentId).then((items) => { if (active) setResults(items); }).catch((reason) => { if (active) setError(reason instanceof Error ? reason.message : "Search failed"); }).finally(() => { if (active) setSearching(false); }), 250);
    return () => { active = false; clearTimeout(timer); };
  }, [documentId, query]);
  const loadMore = async () => {
    if (!hasMore || loadingMoreRef.current) return;
    loadingMoreRef.current = true; setLoadingMore(true);
    try {
      const last = chunks.reduce((max, item) => Math.max(max, item.ordinal), 0);
      const next = await listDocumentChunks(documentId, last);
      setChunks((items) => [...items, ...next.filter((candidate) => !items.some((item) => item.id === candidate.id))]);
      setHasMore(next.length === 50);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Could not load more sections");
    } finally {
      loadingMoreRef.current = false; setLoadingMore(false);
    }
  };
  const loadNearEnd = (element: HTMLElement) => { if (element.scrollHeight - element.scrollTop - element.clientHeight < 160) void loadMore(); };
  const openResult = (result: SearchResult) => { setTocOpen(false); location.hash = `manual/${encodeURIComponent(documentId)}/chunk/${result.chunk_id}`; };
  return <div className="viewer-backdrop"><section ref={dialogRef} className="manual-viewer" role="dialog" aria-modal="true" aria-label={`${manual?.title || documentId} manual viewer`}>
    <header><button className="viewer-toc-button mobile-only" onClick={() => setTocOpen((value) => !value)} aria-expanded={tocOpen}>Sections</button><div><h2>{manual?.title || documentId}</h2><span>{[manual?.id || documentId, manual?.brand, manual?.model, ...(manual?.tags || [])].filter(Boolean).join(" · ")}</span></div><button className="icon-button" onClick={onClose} aria-label="Close manual viewer">×</button></header>
	    <div className={`viewer-layout ${tocOpen ? "toc-open" : ""}`}><nav onScroll={(event) => loadNearEnd(event.currentTarget)}><label className="viewer-search"><span className="sr-only">Search this manual</span><input type="search" value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Search this manual" /></label>{query.trim() ? <div className="viewer-results">{searching && <span>Searching…</span>}{!searching && !results.length && <span>No matching sections</span>}{results.map((result) => <button key={result.chunk_id} onClick={() => openResult(result)}><strong>{result.heading_path || "Untitled section"}</strong><small>{result.page_number ? `Page ${result.page_number} · ` : ""}{result.snippet}</small></button>)}</div> : <>{chunks.map((chunk) => <a className={chunk.id === chunkId ? "active" : ""} key={chunk.id} href={`#manual/${encodeURIComponent(documentId)}/chunk/${chunk.id}`} onClick={() => setTocOpen(false)}>{chunk.heading_path || `Chunk ${chunk.ordinal}`}</a>)}{hasMore && <button className="toc-load-more" onClick={() => void loadMore()} disabled={loadingMore}>{loadingMore ? "Loading sections…" : "Load more sections"}</button>}</>}</nav>
      <main onScroll={(event) => loadNearEnd(event.currentTarget)}>{loading && <p>Loading manual…</p>}{error && <div className="message-error">{error}</div>}{chunks.map((chunk) => <article id={`chunk-${chunk.id}`} className={chunk.id === chunkId ? "target-chunk" : ""} key={chunk.id}>
        <div className="chunk-heading"><div><h3>{chunk.heading_path}</h3><div className="chunk-meta">{chunk.page_number ? `Page ${chunk.page_number} · ` : ""}lines {chunk.start_line}–{chunk.end_line}</div></div><button onClick={() => onAsk(chunk)}>Ask about this section</button></div>
        <div className="markdown"><ReactMarkdown remarkPlugins={[remarkGfm]}>{chunk.text}</ReactMarkdown></div>
        {chunk.images?.map((image) => <img key={image.id} src={`/api/images/${image.id}`} alt={image.alt} loading="lazy" />)}
      </article>)}{hasMore && <button className="load-more" onClick={() => void loadMore()} disabled={loadingMore}>{loadingMore ? "Loading…" : "Load more"}</button>}</main>
    </div>
  </section></div>;
}

function ConfirmDeleteChat({ chat, streaming, onCancel, onConfirm }: { chat: Chat; streaming: boolean; onCancel(): void; onConfirm(): void }) {
  const dialogRef = useDialog<HTMLDivElement>(onCancel);
	  return <div className="modal-backdrop"><div ref={dialogRef} className="confirm-dialog" role="alertdialog" aria-modal="true" aria-labelledby="delete-chat-title">
    <h2 id="delete-chat-title">Delete chat?</h2><p>“{chat.title}” and its messages will be permanently removed.{streaming ? " The active response will be stopped." : ""}</p>
    <div className="confirm-actions"><button onClick={onCancel} autoFocus>Cancel</button><button className="danger-button" onClick={onConfirm}>Delete</button></div>
  </div></div>;
}

function ConfirmDeleteDocument({ document, onCancel, onConfirm }: { document: DocumentInfo; onCancel(): void; onConfirm(): Promise<void> }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const dialogRef = useDialog<HTMLDivElement>(onCancel, !busy);
  const remove = async () => { setBusy(true); setError(""); try { await onConfirm(); } catch (reason) { setError(reason instanceof Error ? reason.message : "Could not remove manual"); setBusy(false); } };
	  return <div className="modal-backdrop"><div ref={dialogRef} className="confirm-dialog" role="alertdialog" aria-modal="true" aria-labelledby="delete-document-title">
    <h2 id="delete-document-title">Remove manual?</h2><p>“{document.title}” and its indexed chunks and images will be permanently removed. Existing chats will switch to all manuals.</p>
    {error && <div className="message-error">{error}</div>}<div className="confirm-actions"><button onClick={onCancel} disabled={busy} autoFocus>Cancel</button><button className="danger-button" onClick={() => void remove()} disabled={busy}>{busy ? "Removing…" : "Remove"}</button></div>
  </div></div>;
}

function ConfirmScopeChange({ current, next, onCancel, onConfirm }: { current: string; next: string; onCancel(): void; onConfirm(): void }) {
  const dialogRef = useDialog<HTMLDivElement>(onCancel);
  return <div className="modal-backdrop"><div ref={dialogRef} className="confirm-dialog" role="alertdialog" aria-modal="true" aria-labelledby="scope-change-title">
    <h2 id="scope-change-title">Change manual scope?</h2><p>This chat currently uses {current}. Switching to {next} changes the sources used for future answers but does not change earlier responses.</p>
    <div className="confirm-actions"><button onClick={onCancel} autoFocus>Cancel</button><button onClick={onConfirm}>Change scope</button></div>
  </div></div>;
}

function DocumentHealthDialog({ document, onClose, onSettings }: { document: DocumentInfo; onClose(): void; onSettings(): void }) {
  const [health, setHealth] = useState<DocumentHealth | null>(null);
  const [error, setError] = useState("");
  useEffect(() => { let active = true; documentHealth(document.id).then((value) => { if (active) setHealth(value); }).catch((reason) => { if (active) setError(reason instanceof Error ? reason.message : "Could not load document health"); }); return () => { active = false; }; }, [document.id]);
  const state = (coverage: { complete: boolean; total: number }) => coverage.total === 0 ? "Empty" : coverage.complete ? "Healthy" : "Incomplete";
  const dialogRef = useDialog<HTMLDivElement>(onClose);
	  return <div className="modal-backdrop"><div ref={dialogRef} className="modal health-dialog" role="dialog" aria-modal="true" aria-labelledby="health-title">
	    <div className="modal-header"><div><h2 id="health-title">Document health</h2><p>{document.title}</p></div><button className="icon-button" onClick={onClose} aria-label="Close document health">×</button></div>
    {!health && !error && <div className="modal-status">Checking document…</div>}{error && <div className="message-error">{error}</div>}
    {health && <><div className="health-counts"><div><strong>{health.chunk_count}</strong><span>Chunks</span></div><div><strong>{health.image_count}</strong><span>Images</span></div></div>
      <div className="health-index"><div><strong>Full-text index</strong><span>{state(health.fts)} · {health.fts.indexed}/{health.fts.total} chunks</span></div><div><strong>Vector index</strong><span>{!health.vectors.configured ? "Not configured" : health.vectors.stale ? `Stale · ${health.vectors.indexed}/${health.vectors.total} chunks` : `${state(health.vectors)} · ${health.vectors.indexed}/${health.vectors.total} chunks`}</span></div></div>
      <section className="health-warnings"><h3>Import warnings</h3>{health.warnings.length ? <ul>{health.warnings.map((warning, index) => <li key={`${warning.code}-${warning.line || 0}-${index}`}>{warning.line ? `Line ${warning.line}: ` : ""}{warning.message}</li>)}</ul> : <p>No import warnings.</p>}</section>
      <div className="modal-footer"><button className="secondary-button" onClick={onSettings}>Open settings</button><button onClick={onClose}>Done</button></div></>}
  </div></div>;
}

function SettingsDialog({ onClose, onNotify, onChanged }: { onClose(): void; onNotify(message: string): void; onChanged(): void }) {
  const [value, setValue] = useState<Settings | null>(null); const [index, setIndex] = useState<EmbeddingIndexStatus | null>(null);
  const [savedValue, setSavedValue] = useState<Settings | null>(null);
  const [status, setStatus] = useState("Loading…"); const [busy, setBusy] = useState(false);
  const refresh = async () => { try { const [settings, indexStatus] = await Promise.all([getSettings(), embeddingIndexStatus()]); setValue(settings); setSavedValue(settings); setIndex(indexStatus); setStatus(""); } catch (error) { setStatus(error instanceof Error ? error.message : "Could not load settings"); } };
  useEffect(() => void refresh(), []);
  const dirty = !!value && !!savedValue && JSON.stringify(value) !== JSON.stringify(savedValue);
  const requestClose = () => { if (!dirty || window.confirm("Discard unsaved settings changes?")) onClose(); };
  useEffect(() => { const warn = (event: BeforeUnloadEvent) => { if (dirty) event.preventDefault(); }; window.addEventListener("beforeunload", warn); return () => window.removeEventListener("beforeunload", warn); }, [dirty]);
  const dialogRef = useDialog<HTMLDivElement>(requestClose, !busy);
  if (!value) return <div className="modal-backdrop"><div ref={dialogRef} className="modal" role="dialog" aria-modal="true" aria-label="Settings"><div className="modal-header"><h2>Settings</h2><button className="icon-button" onClick={onClose} aria-label="Close settings">×</button></div><p>{status}</p></div></div>;
  const changeProvider = (field: string, fieldValue: string) => setValue({ ...value, provider: { ...value.provider, [field]: fieldValue } });
  const changeEmbedding = (field: string, fieldValue: string | number) => setValue({ ...value, embedding: { ...value.embedding, [field]: fieldValue } });
  const save = async () => { setBusy(true); try { const saved = await saveSettings(value); setValue(saved); setSavedValue(saved); setStatus("Settings saved"); onNotify("Settings saved"); onChanged(); setIndex(await embeddingIndexStatus()); } catch (error) { setStatus(error instanceof Error ? error.message : "Save failed"); } finally { setBusy(false); } };
  const test = async (target: "provider" | "embedding") => { setBusy(true); try { const result = await testSettings(target, value); setStatus(`${target} connection OK: ${result.model}${result.dimensions ? ` (${result.dimensions} dimensions)` : ""}`); onNotify(`${target === "provider" ? "Chat" : "Embedding"} connection succeeded`); } catch (error) { setStatus(error instanceof Error ? error.message : "Test failed"); } finally { setBusy(false); } };
  const build = async () => { if (dirty) { setStatus("Save settings before building embeddings"); return; } setBusy(true); try { const result = await buildEmbeddingIndex((indexed, total) => setStatus(`Embedding ${indexed}/${total} chunks…`)); setIndex(result); setStatus("Embedding index complete"); onNotify("Embedding index complete"); onChanged(); } catch (error) { setStatus(error instanceof Error ? error.message : "Index build failed"); } finally { setBusy(false); } };
	  return <div className="modal-backdrop"><div ref={dialogRef} className="modal settings-modal" role="dialog" aria-modal="true" aria-label="Provider settings"><div className="modal-header"><div><h2>Provider settings</h2><p>Saved to config.toml. Environment variables override these values. Embeddings are optional and improve search quality.</p></div><button className="icon-button" onClick={requestClose} disabled={busy} aria-label="Close settings">×</button></div>
    <div className="settings-sections">
      <fieldset><legend>Chat provider</legend><label>Base URL<input value={value.provider.base_url} onChange={(event) => changeProvider("base_url", event.target.value)} /></label><label>Model<input value={value.provider.model} onChange={(event) => changeProvider("model", event.target.value)} /></label><label>Timeout<input value={value.provider.timeout} onChange={(event) => changeProvider("timeout", event.target.value)} /></label><KeyFields settings={value.provider} change={changeProvider} /><button onClick={() => void test("provider")} disabled={busy}>Test chat connection</button></fieldset>
      <fieldset><legend>Embedding provider</legend><label>Base URL<input value={value.embedding.base_url} onChange={(event) => changeEmbedding("base_url", event.target.value)} /></label><label>Model<input value={value.embedding.model} onChange={(event) => changeEmbedding("model", event.target.value)} /></label><div className="settings-grid"><label>Dimensions<input type="number" value={value.embedding.dimensions} onChange={(event) => changeEmbedding("dimensions", Number(event.target.value))} /></label><label>Batch size<input type="number" value={value.embedding.batch_size} onChange={(event) => changeEmbedding("batch_size", Number(event.target.value))} /></label></div><label>Query instruction<textarea value={value.embedding.query_instruction} onChange={(event) => changeEmbedding("query_instruction", event.target.value)} /></label><KeyFields settings={value.embedding} change={changeEmbedding} /><button onClick={() => void test("embedding")} disabled={busy}>Test embedding connection</button></fieldset>
    </div>
    {value.environment_overrides.length > 0 && <p className="settings-warning">Environment overrides: {value.environment_overrides.join(", ")}</p>}
    <div className="index-status"><strong>Vector index</strong><span>{index?.complete ? `${index.indexed}/${index.total} chunks indexed` : `Stale · ${index?.indexed || 0}/${index?.total || 0}`}</span><button onClick={() => void build()} disabled={busy || dirty} title={dirty ? "Save settings before building embeddings" : undefined}>Build embeddings</button></div>
    {status && <p className="modal-status">{status}</p>}<div className="modal-footer"><span>{dirty ? "Unsaved changes · " : ""}Keys are never returned by the API.</span><button onClick={() => void save()} disabled={busy || !dirty}>Save settings</button></div>
  </div></div>;
}

function KeyFields({ settings, change }: { settings: { api_key?: string; api_key_set: boolean; api_key_action?: string }; change(field: string, value: string): void }) {
  return <div className="key-fields"><label>API key action<select value={settings.api_key_action || "preserve"} onChange={(event) => change("api_key_action", event.target.value)}><option value="preserve">Preserve {settings.api_key_set ? "saved key" : "empty key"}</option><option value="replace">Replace key</option><option value="clear">Clear key</option></select></label>{settings.api_key_action === "replace" && <label>New API key<input type="password" value={settings.api_key || ""} onChange={(event) => change("api_key", event.target.value)} /></label>}</div>;
}

type ImportFileStatus = { state: "ready" | "importing" | "success" | "error"; detail: string };
const importFileKey = (file: File) => `${file.name}-${file.size}-${file.lastModified}`;

function ImportDialog({ onClose, onImported }: { onClose(): void; onImported(): Promise<void> }) {
  const [files, setFiles] = useState<File[]>([]);
  const [dragging, setDragging] = useState(false);
  const [replace, setReplace] = useState(false);
  const [metadata, setMetadata] = useState({ id: "", brand: "", model: "", tags: "" });
  const [statuses, setStatuses] = useState<Record<string, ImportFileStatus>>({});
  const [selectionError, setSelectionError] = useState("");
  const [busy, setBusy] = useState(false);
  const totalSize = useMemo(() => files.reduce((sum, file) => sum + file.size, 0), [files]);
  const completed = files.filter((file) => ["success", "error"].includes(statuses[importFileKey(file)]?.state)).length;
  const failed = files.filter((file) => statuses[importFileKey(file)]?.state === "error").length;
  const succeeded = files.filter((file) => statuses[importFileKey(file)]?.state === "success").length;
  const dialogRef = useDialog<HTMLDivElement>(onClose, !busy);
  const selectFiles = (selected: File[]) => {
    const markdown = selected.filter((file) => /\.(?:md|markdown)$/i.test(file.name) || file.type === "text/markdown");
    setFiles(markdown);
    setStatuses(Object.fromEntries(markdown.map((file) => [importFileKey(file), { state: "ready", detail: "Ready" }])));
    setSelectionError(markdown.length === selected.length ? "" : "Only Markdown files can be imported.");
  };

  const runImport = async () => {
    setBusy(true);
    const pending = files.filter((file) => statuses[importFileKey(file)]?.state !== "success");
    for (let index = 0; index < pending.length; index++) {
      const file = pending[index];
      const key = importFileKey(file);
      setStatuses((value) => ({ ...value, [key]: { state: "importing", detail: `Importing ${index + 1} of ${pending.length}…` } }));
      try {
        const result = await importMarkdown(file, replace, { ...metadata, id: files.length === 1 ? metadata.id.trim() : "", brand: metadata.brand.trim(), model: metadata.model.trim(), tags: metadata.tags.trim() });
        const imageDetail = result.images === 1 ? "1 image" : `${result.images} images`;
        const warningDetail = result.warnings?.length ? ` · ${result.warnings.length} warning${result.warnings.length === 1 ? "" : "s"}` : "";
        setStatuses((value) => ({ ...value, [key]: { state: "success", detail: `${result.chunks} chunks · ${imageDetail}${warningDetail}` } }));
      } catch (error) {
        setStatuses((value) => ({ ...value, [key]: { state: "error", detail: error instanceof Error ? error.message : "Import failed" } }));
      }
    }
    try {
      await onImported();
    } catch (error) {
      setSelectionError(error instanceof Error ? `Files imported, but the manual list could not refresh: ${error.message}` : "Files imported, but the manual list could not refresh.");
    } finally {
      setBusy(false);
    }
  };

	  return <div className="modal-backdrop" role="presentation"><div ref={dialogRef} className="modal" role="dialog" aria-modal="true" aria-label="Import Markdown">
	    <div className="modal-header"><div><h2>Import Markdown</h2><p>Embedded PNG, JPEG, GIF, and WebP images are stored with their source sections.</p></div><button className="icon-button" onClick={onClose} disabled={busy} aria-label="Close import dialog">×</button></div>
    <div className="import-metadata">
      <label>Document ID<input value={metadata.id} disabled={busy || files.length > 1} placeholder="Generated from title when empty" onChange={(event) => setMetadata({ ...metadata, id: event.target.value })} /><small>{files.length > 1 ? "A custom ID can only be used when importing one file." : "Stable identifier used by links and replacement imports."}</small></label>
      <label>Brand<input value={metadata.brand} disabled={busy} placeholder="e.g. Elektron" onChange={(event) => setMetadata({ ...metadata, brand: event.target.value })} /></label>
      <label>Model<input value={metadata.model} disabled={busy} placeholder="e.g. Analog Four MKII" onChange={(event) => setMetadata({ ...metadata, model: event.target.value })} /></label>
      <label>Tags<input value={metadata.tags} disabled={busy} placeholder="synthesizer, reference, studio" onChange={(event) => setMetadata({ ...metadata, tags: event.target.value })} /><small>Separate tags with commas.</small></label>
    </div>
    <label className={`file-drop${dragging ? " dragging" : ""}`}
      onDragEnter={(event) => { event.preventDefault(); setDragging(true); }}
      onDragOver={(event) => { event.preventDefault(); event.dataTransfer.dropEffect = "copy"; setDragging(true); }}
      onDragLeave={(event) => { event.preventDefault(); if (!event.currentTarget.contains(event.relatedTarget as Node | null)) setDragging(false); }}
      onDrop={(event) => { event.preventDefault(); event.stopPropagation(); setDragging(false); selectFiles(Array.from(event.dataTransfer.files)); }}>
      <input type="file" accept=".md,.markdown,text/markdown" multiple onChange={(event) => selectFiles(Array.from(event.target.files || []))} />
      <strong>{dragging ? "Drop Markdown files here" : "Choose or drop Markdown files"}</strong><span>Up to 100 MiB per upload</span>
    </label>
    {selectionError && <div className="message-error">{selectionError}</div>}
    {files.length > 0 && <><div className="import-progress"><progress max={files.length} value={completed} /><span>{busy ? `${completed} of ${files.length} complete` : completed === files.length ? `${succeeded} imported${failed ? ` · ${failed} failed` : ""}` : `${files.length} selected`}</span></div>
      <div className="import-list">{files.map((file) => {
        const status = statuses[importFileKey(file)] || { state: "ready", detail: "Ready" };
        return <div className={`import-${status.state}`} key={importFileKey(file)}><span><strong>{file.name}</strong> <small>{(file.size / 1024).toFixed(1)} KiB</small></span><span className="import-detail">{status.state === "success" ? "✓ " : status.state === "error" ? "! " : ""}{status.detail}</span></div>;
      })}</div></>}
    <label className="checkbox"><input type="checkbox" checked={replace} onChange={(event) => setReplace(event.target.checked)} /> Replace manuals with matching IDs</label>
    <div className="modal-footer"><span>{files.length ? `${files.length} file${files.length > 1 ? "s" : ""} · ${(totalSize / 1024 / 1024).toFixed(1)} MiB` : "No files selected"}</span><button onClick={() => void runImport()} disabled={!files.length || busy || (completed === files.length && failed === 0)}>{busy ? "Importing…" : failed ? "Retry failed" : completed === files.length ? "Imported" : "Import"}</button></div>
  </div></div>;
}
