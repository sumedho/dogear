import { useEffect, useRef, useState } from "react";
import { embeddingIndexStatus, getSettings, listDocuments, removeDocument as removeDocumentAPI, streamAsk } from "./api";
import { SettingsDialog } from "./SettingsDialog";
import { AnswerMarkdown, sourceDescription } from "./AnswerMarkdown";
import { ManualViewer } from "./ManualViewer";
import { ImportDialog } from "./ImportDialog";
import { exportChats, loadChats, mergeChatBackup, newChat, saveChats } from "./storage";
import type { AskResult, Chat, ChatMessage, DisplayImage, DocumentChunk, DocumentInfo, ResponseMode } from "./types";
import { useChatRequests } from "./useChatRequests";
import { ConfirmDeleteChat, ConfirmDeleteDocument, ConfirmScopeChange, DocumentHealthDialog, ManualDialog } from "./AppDialogs";

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
  const requests = useChatRequests();
  const [toast, setToast] = useState<Toast | null>(null);
  const [chatFilter, setChatFilter] = useState("");
  const [readiness, setReadiness] = useState<{ label: string; state: "ready" | "limited" | "needed"; detail: string }>({ label: "Checking…", state: "limited", detail: "Checking configuration" });
  const [pendingScope, setPendingScope] = useState<string | null>(null);
  const [showJumpToLatest, setShowJumpToLatest] = useState(false);
  const [nextMode, setNextMode] = useState<ResponseMode>("auto");
  const messagesRef = useRef<HTMLElement>(null);
  const composerRef = useRef<HTMLTextAreaElement>(null);
  const chatFilterRef = useRef<HTMLInputElement>(null);
  const chatImportRef = useRef<HTMLInputElement>(null);
  const cancelRenameRef = useRef(false);
  const current = chats.find((chat) => chat.id === activeId) || chats[0];
  const currentController = requests.controllerFor(current.id);

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
      else if (index.building) setReadiness({ label: "Indexing", state: "limited", detail: `${index.progress_indexed || 0}/${index.progress_total || index.total} chunks` });
      else if (index.stale || !index.complete) setReadiness({ label: "Index stale", state: "limited", detail: index.last_error || "Rebuild the embedding index" });
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
    if (messages) {
      const nearBottom = messages.scrollHeight - messages.scrollTop - messages.clientHeight < 120;
      if (nearBottom || !showJumpToLatest) messages.scrollTo({ top: messages.scrollHeight, behavior: "smooth" });
      else setShowJumpToLatest(true);
    }
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

  const runAnswer = async (chat: Chat, question: string, history: Array<{ role: string; content: string }>, assistantId: string, mode: ResponseMode) => {
    const abort = requests.begin(chat.id);
    if (!abort) return;
    try {
      await streamAsk(
        { question, doc: chat.documentId, limit: 8, mode, history },
        {
          onDelta: (content) => updateChat(chat.id, (value) => ({ ...value, messages: value.messages.map((message) => message.id === assistantId ? { ...message, content: message.content + content } : message), updatedAt: Date.now() })),
          onStatus: (statusText) => updateChat(chat.id, (value) => ({ ...value, messages: value.messages.map((message) => message.id === assistantId ? { ...message, statusText } : message) })),
          onResult: (result: AskResult) => updateChat(chat.id, (value) => ({ ...value, messages: value.messages.map((message) => message.id === assistantId ? { ...message, content: result.answer, status: "done", mode: result.mode, statusText: undefined, sources: result.sources, retrieval: result.retrieval, images: result.images, error: undefined } : message), updatedAt: Date.now() })),
        },
        abort.signal,
      );
    } catch (error) {
      const cancelled = error instanceof DOMException && error.name === "AbortError";
      updateChat(chat.id, (value) => ({ ...value, messages: value.messages.map((message) => message.id === assistantId ? { ...message, status: cancelled ? "cancelled" : "error", error: cancelled ? "Stopped" : error instanceof Error ? error.message : "Request failed" } : message) }));
    } finally {
      requests.finish(chat.id, abort);
    }
  };

  const send = async () => {
    const question = current.draft.trim();
    if (!question || currentController) return;
    const chat = current;
    const userMessage: ChatMessage = { id: crypto.randomUUID(), role: "user", content: question, status: "done" };
    const assistantId = crypto.randomUUID();
    const mode = nextMode;
    const assistantMessage: ChatMessage = { id: assistantId, role: "assistant", content: "", status: "streaming", statusText: mode === "guide" ? "Planning guide…" : undefined };
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
    setNextMode("auto");
    await runAnswer(chat, question, history, assistantId, mode);
  };

  const retryAnswer = async (assistantId: string) => {
    if (currentController) return;
    const assistantIndex = current.messages.findIndex((message) => message.id === assistantId);
    const userIndex = previousUserIndex(current.messages, assistantIndex);
    if (userIndex < 0) return;
    const question = current.messages[userIndex].content;
    const history = current.messages.slice(0, userIndex).filter((message) => message.content && message.status !== "error" && message.status !== "cancelled").map(({ role, content }) => ({ role, content }));
    const retryMode = current.messages[assistantIndex].mode === "guide" ? "guide" : "auto";
    updateChat(current.id, (chat) => ({ ...chat, messages: chat.messages.map((message) => message.id === assistantId ? { ...message, content: "", status: "streaming", statusText: retryMode === "guide" ? "Planning guide…" : undefined, error: undefined, sources: undefined, retrieval: undefined, images: undefined } : message), updatedAt: Date.now() }));
    await runAnswer(current, question, history, assistantId, retryMode);
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

  const copyText = async (value: string, label: string) => {
    try {
      await navigator.clipboard.writeText(value);
      setToast({ message: `${label} copied` });
    } catch {
      setToast({ message: `Could not copy ${label.toLowerCase()}` });
    }
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

        <section ref={messagesRef} className="messages" onScroll={(event) => {
          const element = event.currentTarget;
          setShowJumpToLatest(element.scrollHeight - element.scrollTop - element.clientHeight >= 120);
        }}>
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
                <div className="message-author">{message.role === "user" ? "You" : "DogEar"}{message.mode === "guide" && <span className="mode-badge">Guide</span>}</div>
                {message.role === "assistant" ? (
                  <div className="markdown"><AnswerMarkdown message={message} onOpen={openViewer} /></div>
                ) : <div className="user-text">{message.content}</div>}
				{message.images && message.images.length > 0 && <AnswerImages images={message.images} onOpen={openViewer} />}
                {message.status === "streaming" && <span className="streaming-cursor" aria-label="Streaming" />}
                {message.error && <div className={`message-error ${message.status === "cancelled" ? "muted" : ""}`}>{message.error}</div>}
                {message.role === "assistant" && message.retrieval?.fallback_reason && <div className="retrieval-note"><span>Used full-text search: {message.retrieval.fallback_reason}.</span><button onClick={() => setSettingsOpen(true)}>{message.retrieval.fallback_reason.includes("index") ? "Rebuild index" : "Configure embeddings"}</button></div>}
                {message.sources && message.sources.length > 0 && <SourceCards message={message} onOpen={openViewer} onCopy={(value) => void copyText(value, "Citation")} />}
                {message.role === "assistant" && message.status !== "streaming" && <div className="message-actions">
                  <button onClick={() => void copyText(message.content, "Answer")} disabled={!message.content}>Copy answer</button>
                  <button onClick={() => void retryAnswer(message.id)} disabled={!!currentController}>{message.status === "done" ? "Regenerate" : "Retry"}</button>
                  <button onClick={() => editQuestion(message.id)}>Edit question</button>
                  {message.status === "error" && <button onClick={() => setSettingsOpen(true)}>Open settings</button>}
                </div>}
              </div>
            </article>
          ))}
          <div className="sr-only" role="status" aria-live="polite">{currentController ? "DogEar is generating a response" : ""}</div>
        </section>

        {showJumpToLatest && <button className="jump-latest" onClick={() => { const element = messagesRef.current; element?.scrollTo({ top: element.scrollHeight, behavior: "smooth" }); setShowJumpToLatest(false); }}>Jump to latest ↓</button>}

        <div className="composer-wrap">
          <div className="composer">
            <button className={`guide-toggle ${nextMode === "guide" ? "active" : ""}`} onClick={() => setNextMode((mode) => mode === "guide" ? "auto" : "guide")} aria-pressed={nextMode === "guide"} title="Synthesize a multi-section guide for the next message">Guide</button>
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
            {currentController ? <button className="send-button stop" onClick={() => currentController.abort()} aria-label="Stop response">■</button>
              : <button className="send-button" onClick={() => void send()} disabled={!current.draft.trim() || !documents.length} aria-label="Send message">↑</button>}
          </div>
          <div className="composer-note">Answers are grounded in retrieved manual sections. Verify important details against the cited sources.</div>
        </div>
      </main>

      {browseOpen && <ManualDialog documents={documents} loading={loadingDocs} error={docsError} onRefresh={refreshDocuments} onSelect={(id) => { selectDocument(id); setBrowseOpen(false); }} onOpen={(id) => { setBrowseOpen(false); openViewer(id); }} onHealth={(document) => { setBrowseOpen(false); setHealthDocument(document); }} onRemove={(document) => { setBrowseOpen(false); setDeleteDocument(document); }} onClose={() => setBrowseOpen(false)} />}
      {importOpen && <ImportDialog onClose={() => setImportOpen(false)} onImported={async () => { await refreshDocuments(); await refreshReadiness(); setToast({ message: "Manual library updated" }); }} />}
			{settingsOpen && <SettingsDialog onClose={() => setSettingsOpen(false)} onNotify={(message) => setToast({ message })} onChanged={() => void refreshReadiness()} />}
		{viewer && <ManualViewer manual={documents.find((item) => item.id === viewer.documentId)} documentId={viewer.documentId} chunkId={viewer.chunkId} onAsk={askAboutSection} onClose={closeViewer} />}
      {deleteChat && <ConfirmDeleteChat chat={deleteChat} streaming={requests.activeChats.has(deleteChat.id)} onCancel={() => setDeleteChat(null)} onConfirm={() => { requests.stop(deleteChat.id); deleteChatWithUndo(deleteChat); setDeleteChat(null); }} />}
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

function SourceCards({ message, onOpen, onCopy }: { message: ChatMessage; onOpen(documentId: string, chunkId?: number): void; onCopy(value: string): void }) {
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
              <button className="copy-citation" onClick={() => onCopy(`${source.label} ${sourceDescription(source)}`)}>Copy citation</button>
            </div>
          );
        })}
      </div>
    </details>
  );
}
