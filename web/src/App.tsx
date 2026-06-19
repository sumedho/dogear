import { useEffect, useMemo, useRef, useState } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { buildEmbeddingIndex, embeddingIndexStatus, getDocumentChunk, getSettings, importMarkdown, listDocumentChunks, listDocuments, saveSettings, streamAsk, testSettings } from "./api";
import { loadChats, newChat, saveChats } from "./storage";
import type { AskResult, Chat, ChatMessage, DocumentChunk, DocumentInfo, EmbeddingIndexStatus, Settings, SourceRef } from "./types";

function sourceDescription(source: SourceRef): string {
  const parts = [source.title];
  if (source.page_number) parts.push(`p.${source.page_number}`);
  if (source.heading_path) parts.push(source.heading_path);
  parts.push(`lines ${source.start_line}–${source.end_line}`);
  return parts.join(" · ");
}

function initialChats(): Chat[] {
  const saved = loadChats();
  return saved.length ? saved : [newChat()];
}

export default function App() {
  const [chats, setChats] = useState<Chat[]>(initialChats);
  const [activeId, setActiveId] = useState(() => chats[0].id);
  const [documents, setDocuments] = useState<DocumentInfo[]>([]);
  const [draft, setDraft] = useState("");
  const [browseOpen, setBrowseOpen] = useState(false);
  const [importOpen, setImportOpen] = useState(false);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [viewer, setViewer] = useState<{ documentId: string; chunkId?: number } | null>(null);
  const [sidebarOpen, setSidebarOpen] = useState(false);
  const [loadingDocs, setLoadingDocs] = useState(true);
  const [docsError, setDocsError] = useState("");
  const [controller, setController] = useState<AbortController | null>(null);
  const endRef = useRef<HTMLDivElement>(null);
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

  useEffect(() => void refreshDocuments(), []);
	useEffect(() => {
		const restore = () => { const match = location.hash.match(/^#manual\/([^/]+)(?:\/chunk\/(\d+))?/); setViewer(match ? { documentId: decodeURIComponent(match[1]), chunkId: match[2] ? Number(match[2]) : undefined } : null); };
		restore(); window.addEventListener("hashchange", restore); return () => window.removeEventListener("hashchange", restore);
	}, []);
  useEffect(() => saveChats(chats), [chats]);
  useEffect(() => endRef.current?.scrollIntoView({ behavior: "smooth" }), [current?.messages]);

  const updateChat = (chatId: string, update: (chat: Chat) => Chat) => {
    setChats((items) => items.map((chat) => (chat.id === chatId ? update(chat) : chat)));
  };

  const createChat = () => {
    const chat = newChat();
    setChats((items) => [chat, ...items]);
    setActiveId(chat.id);
    setDraft("");
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
    updateChat(current.id, (chat) => ({ ...chat, documentId, updatedAt: Date.now() }));
  };

  const send = async () => {
    const question = draft.trim();
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
      messages: [...value.messages, userMessage, assistantMessage],
      updatedAt: Date.now(),
    }));
    setDraft("");
    const abort = new AbortController();
    setController(abort);
    try {
      await streamAsk(
        { question, doc: chat.documentId, limit: 8, history },
        {
          onDelta: (content) => updateChat(chat.id, (value) => ({
            ...value,
            messages: value.messages.map((message) => message.id === assistantId ? { ...message, content: message.content + content } : message),
            updatedAt: Date.now(),
          })),
          onResult: (result: AskResult) => updateChat(chat.id, (value) => ({
            ...value,
            messages: value.messages.map((message) => message.id === assistantId ? {
              ...message,
              content: result.answer,
              status: "done",
              sources: result.sources,
              retrieval: result.retrieval,
            } : message),
            updatedAt: Date.now(),
          })),
        },
        abort.signal,
      );
    } catch (error) {
      const cancelled = error instanceof DOMException && error.name === "AbortError";
      updateChat(chat.id, (value) => ({
        ...value,
        messages: value.messages.map((message) => message.id === assistantId ? {
          ...message,
          status: cancelled ? "cancelled" : "error",
          error: cancelled ? "Stopped" : error instanceof Error ? error.message : "Request failed",
        } : message),
      }));
    } finally {
      setController(null);
    }
  };

  const activeDocument = documents.find((document) => document.id === current.documentId);
	const openViewer = (documentId: string, chunkId?: number) => { location.hash = `manual/${encodeURIComponent(documentId)}${chunkId ? `/chunk/${chunkId}` : ""}`; };

  return (
    <div className="app-shell">
      <aside className={`sidebar ${sidebarOpen ? "sidebar-open" : ""}`}>
        <div className="brand-row">
          <div className="brand-mark">D</div>
          <strong>Dogear</strong>
          <button className="icon-button mobile-only" onClick={() => setSidebarOpen(false)} aria-label="Close sidebar">×</button>
        </div>
        <button className="new-chat" onClick={createChat}><span>＋</span> New chat</button>
        <div className="library-actions">
          <button onClick={() => setBrowseOpen(true)}>▤ <span>Browse manuals</span></button>
          <button onClick={() => setImportOpen(true)}>⇧ <span>Import Markdown</span></button>
			<button onClick={() => setSettingsOpen(true)}>⚙ <span>Settings</span></button>
        </div>
        <div className="sidebar-label">Chats</div>
        <nav className="chat-list">
          {chats.map((chat) => (
            <div className={`chat-row ${chat.id === current.id ? "active" : ""}`} key={chat.id}>
              <button className="chat-select" onClick={() => { setActiveId(chat.id); setSidebarOpen(false); }}>{chat.title}</button>
              <button className="delete-chat" onClick={() => removeChat(chat.id)} aria-label={`Delete ${chat.title}`}>×</button>
            </div>
          ))}
        </nav>
        <div className="sidebar-footer">Local manual assistant</div>
      </aside>

      <main className="chat-main">
        <header className="topbar">
          <button className="icon-button mobile-only" onClick={() => setSidebarOpen(true)} aria-label="Open sidebar">☰</button>
          <select value={current.documentId} onChange={(event) => selectDocument(event.target.value)} aria-label="Manual for this chat">
            <option value="">All manuals</option>
            {documents.map((document) => <option key={document.id} value={document.id}>{document.title}</option>)}
          </select>
          <span className="topbar-status">{activeDocument ? `${activeDocument.chunk_count} indexed chunks` : documents.length ? `${documents.length} manuals` : "No manuals"}</span>
        </header>

        <section className="messages" aria-live="polite">
          {!current.messages.length && (
            <div className="empty-state">
              <div className="empty-logo">D</div>
              <h1>Ask your manuals</h1>
              <p>{documents.length ? "Choose a manual or search across all of them, then ask a question." : "Import a Markdown manual to get started."}</p>
              {!documents.length && <button onClick={() => setImportOpen(true)}>Import Markdown</button>}
            </div>
          )}
          {current.messages.map((message) => (
            <article className={`message message-${message.role}`} key={message.id}>
              <div className="avatar">{message.role === "user" ? "Y" : "D"}</div>
              <div className="message-body">
                <div className="message-author">{message.role === "user" ? "You" : "Dogear"}</div>
                {message.role === "assistant" ? (
                  <div className="markdown"><ReactMarkdown remarkPlugins={[remarkGfm]}>{message.content || (message.status === "streaming" ? "Searching the manual…" : "")}</ReactMarkdown></div>
                ) : <div className="user-text">{message.content}</div>}
                {message.status === "streaming" && <span className="streaming-cursor" aria-label="Streaming" />}
                {message.error && <div className={`message-error ${message.status === "cancelled" ? "muted" : ""}`}>{message.error}</div>}
                {message.sources && message.sources.length > 0 && <SourceCards message={message} onOpen={openViewer} />}
              </div>
            </article>
          ))}
          <div ref={endRef} />
        </section>

        <div className="composer-wrap">
          <div className="composer">
            <textarea
              value={draft}
              onChange={(event) => setDraft(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === "Enter" && !event.shiftKey) {
                  event.preventDefault();
                  void send();
                }
              }}
              placeholder={documents.length ? "Message Dogear" : "Import a manual before asking"}
              disabled={!documents.length}
              rows={1}
            />
            {controller ? <button className="send-button stop" onClick={() => controller.abort()} aria-label="Stop response">■</button>
              : <button className="send-button" onClick={() => void send()} disabled={!draft.trim() || !documents.length} aria-label="Send message">↑</button>}
          </div>
          <div className="composer-note">Answers are grounded in retrieved manual sections. Verify important details against the cited sources.</div>
        </div>
      </main>

      {browseOpen && <ManualDialog documents={documents} loading={loadingDocs} error={docsError} onRefresh={refreshDocuments} onSelect={(id) => { selectDocument(id); setBrowseOpen(false); }} onOpen={(id) => { setBrowseOpen(false); openViewer(id); }} onClose={() => setBrowseOpen(false)} />}
      {importOpen && <ImportDialog onClose={() => setImportOpen(false)} onImported={refreshDocuments} />}
		{settingsOpen && <SettingsDialog onClose={() => setSettingsOpen(false)} />}
		{viewer && <ManualViewer manual={documents.find((item) => item.id === viewer.documentId)} documentId={viewer.documentId} chunkId={viewer.chunkId} onClose={() => { history.pushState(null, "", location.pathname + location.search); setViewer(null); }} />}
      {sidebarOpen && <button className="sidebar-scrim" onClick={() => setSidebarOpen(false)} aria-label="Close sidebar" />}
    </div>
  );
}

function SourceCards({ message, onOpen }: { message: ChatMessage; onOpen(documentId: string, chunkId?: number): void }) {
  const blocks = message.retrieval?.blocks || [];
  return (
    <details className="sources" open>
      <summary>{message.sources?.length} sources</summary>
      <div className="source-grid">
        {message.sources?.map((source) => {
          const block = blocks.find((item) => item.source.label === source.label);
          return (
            <button className="source-card" key={`${source.document_id}-${source.label}`} onClick={() => onOpen(source.document_id, source.chunk_id)}>
              <div className="source-title"><span>{source.label}</span> {sourceDescription(source)}</div>
              {block?.text && <p>{block.text}</p>}
              {block?.images?.map((image) => <img key={image.id} src={`/api/images/${image.id}`} alt={image.alt} loading="lazy" />)}
            </button>
          );
        })}
      </div>
    </details>
  );
}

function ManualDialog({ documents, loading, error, onRefresh, onSelect, onOpen, onClose }: {
  documents: DocumentInfo[]; loading: boolean; error: string; onRefresh(): Promise<void>; onSelect(id: string): void; onOpen(id: string): void; onClose(): void;
}) {
  return <div className="modal-backdrop" role="presentation"><div className="modal" role="dialog" aria-modal="true" aria-label="Manual library">
    <div className="modal-header"><div><h2>Manual library</h2><p>Select the manual used by the current chat.</p></div><button className="icon-button" onClick={onClose}>×</button></div>
    {loading && <div className="modal-status">Loading manuals…</div>}
    {error && <div className="message-error">{error} <button onClick={() => void onRefresh()}>Retry</button></div>}
    {!loading && !error && <div className="manual-list">
      <button className="manual-item" onClick={() => onSelect("")}><strong>All manuals</strong><span>Search every imported document</span></button>
      {documents.map((document) => <div className="manual-item" key={document.id}>
        <strong>{document.title}</strong><span>{[document.brand, document.model].filter(Boolean).join(" ") || document.id} · {document.chunk_count} chunks · {document.page_count} pages</span>
		<div><button onClick={() => onSelect(document.id)}>Use in chat</button><button onClick={() => onOpen(document.id)}>Open manual</button></div>
      </div>)}
    </div>}
  </div></div>;
}

function ManualViewer({ manual, documentId, chunkId, onClose }: { manual?: DocumentInfo; documentId: string; chunkId?: number; onClose(): void }) {
  const [chunks, setChunks] = useState<DocumentChunk[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  useEffect(() => {
    let active = true; setLoading(true);
    Promise.all([listDocumentChunks(documentId), chunkId ? getDocumentChunk(documentId, chunkId) : Promise.resolve(undefined)]).then(([page, target]) => {
      if (!active) return; const combined = target && !page.some((item) => item.id === target.id) ? [target, ...page] : page; setChunks(combined); setLoading(false);
      setTimeout(() => document.getElementById(`chunk-${chunkId}`)?.scrollIntoView({ behavior: "smooth", block: "start" }), 50);
    }).catch((reason) => { if (active) { setError(reason instanceof Error ? reason.message : "Could not load manual"); setLoading(false); } });
    return () => { active = false; };
  }, [documentId, chunkId]);
  const loadMore = async () => { const last = chunks.reduce((max, item) => Math.max(max, item.ordinal), 0); const next = await listDocumentChunks(documentId, last); setChunks((items) => [...items, ...next.filter((candidate) => !items.some((item) => item.id === candidate.id))]); };
  return <div className="viewer-backdrop"><section className="manual-viewer" role="dialog" aria-modal="true">
    <header><div><h2>{manual?.title || documentId}</h2><span>{manual?.brand} {manual?.model}</span></div><button className="icon-button" onClick={onClose}>×</button></header>
    <div className="viewer-layout"><nav>{chunks.map((chunk) => <a key={chunk.id} href={`#manual/${encodeURIComponent(documentId)}/chunk/${chunk.id}`}>{chunk.heading_path || `Chunk ${chunk.ordinal}`}</a>)}</nav>
      <main>{loading && <p>Loading manual…</p>}{error && <div className="message-error">{error}</div>}{chunks.map((chunk) => <article id={`chunk-${chunk.id}`} className={chunk.id === chunkId ? "target-chunk" : ""} key={chunk.id}>
        <h3>{chunk.heading_path}</h3><div className="chunk-meta">{chunk.page_number ? `Page ${chunk.page_number} · ` : ""}lines {chunk.start_line}–{chunk.end_line}</div>
        <div className="markdown"><ReactMarkdown remarkPlugins={[remarkGfm]}>{chunk.text}</ReactMarkdown></div>
        {chunk.images?.map((image) => <img key={image.id} src={`/api/images/${image.id}`} alt={image.alt} loading="lazy" />)}
      </article>)}{chunks.length > 0 && <button className="load-more" onClick={() => void loadMore()}>Load more</button>}</main>
    </div>
  </section></div>;
}

function SettingsDialog({ onClose }: { onClose(): void }) {
  const [value, setValue] = useState<Settings | null>(null); const [index, setIndex] = useState<EmbeddingIndexStatus | null>(null);
  const [status, setStatus] = useState("Loading…"); const [busy, setBusy] = useState(false);
  const refresh = async () => { try { const [settings, indexStatus] = await Promise.all([getSettings(), embeddingIndexStatus()]); setValue(settings); setIndex(indexStatus); setStatus(""); } catch (error) { setStatus(error instanceof Error ? error.message : "Could not load settings"); } };
  useEffect(() => void refresh(), []);
  if (!value) return <div className="modal-backdrop"><div className="modal"><div className="modal-header"><h2>Settings</h2><button className="icon-button" onClick={onClose}>×</button></div><p>{status}</p></div></div>;
  const changeProvider = (field: string, fieldValue: string) => setValue({ ...value, provider: { ...value.provider, [field]: fieldValue } });
  const changeEmbedding = (field: string, fieldValue: string | number) => setValue({ ...value, embedding: { ...value.embedding, [field]: fieldValue } });
  const save = async () => { setBusy(true); try { setValue(await saveSettings(value)); setStatus("Settings saved"); await refresh(); } catch (error) { setStatus(error instanceof Error ? error.message : "Save failed"); } finally { setBusy(false); } };
  const test = async (target: "provider" | "embedding") => { setBusy(true); try { const result = await testSettings(target); setStatus(`${target} connection OK: ${result.model}${result.dimensions ? ` (${result.dimensions} dimensions)` : ""}`); } catch (error) { setStatus(error instanceof Error ? error.message : "Test failed"); } finally { setBusy(false); } };
  const build = async () => { setBusy(true); try { const result = await buildEmbeddingIndex((indexed, total) => setStatus(`Embedding ${indexed}/${total} chunks…`)); setIndex(result); setStatus("Embedding index complete"); } catch (error) { setStatus(error instanceof Error ? error.message : "Index build failed"); } finally { setBusy(false); } };
  return <div className="modal-backdrop"><div className="modal settings-modal" role="dialog" aria-modal="true"><div className="modal-header"><div><h2>Provider settings</h2><p>Saved to config.toml. Environment variables override these values.</p></div><button className="icon-button" onClick={onClose}>×</button></div>
    <fieldset><legend>Chat provider</legend><label>Base URL<input value={value.provider.base_url} onChange={(event) => changeProvider("base_url", event.target.value)} /></label><label>Model<input value={value.provider.model} onChange={(event) => changeProvider("model", event.target.value)} /></label><label>Timeout<input value={value.provider.timeout} onChange={(event) => changeProvider("timeout", event.target.value)} /></label><KeyFields settings={value.provider} change={changeProvider} /><button onClick={() => void test("provider")} disabled={busy}>Test chat connection</button></fieldset>
    <fieldset><legend>Embedding provider</legend><label>Base URL<input value={value.embedding.base_url} onChange={(event) => changeEmbedding("base_url", event.target.value)} /></label><label>Model<input value={value.embedding.model} onChange={(event) => changeEmbedding("model", event.target.value)} /></label><div className="settings-grid"><label>Dimensions<input type="number" value={value.embedding.dimensions} onChange={(event) => changeEmbedding("dimensions", Number(event.target.value))} /></label><label>Batch size<input type="number" value={value.embedding.batch_size} onChange={(event) => changeEmbedding("batch_size", Number(event.target.value))} /></label></div><label>Query instruction<textarea value={value.embedding.query_instruction} onChange={(event) => changeEmbedding("query_instruction", event.target.value)} /></label><KeyFields settings={value.embedding} change={changeEmbedding} /><button onClick={() => void test("embedding")} disabled={busy}>Test embedding connection</button></fieldset>
    {value.environment_overrides.length > 0 && <p className="settings-warning">Environment overrides: {value.environment_overrides.join(", ")}</p>}
    <div className="index-status"><strong>Vector index</strong><span>{index?.complete ? `${index.indexed}/${index.total} chunks indexed` : `Stale · ${index?.indexed || 0}/${index?.total || 0}`}</span><button onClick={() => void build()} disabled={busy}>Build embeddings</button></div>
    {status && <p className="modal-status">{status}</p>}<div className="modal-footer"><span>Keys are never returned by the API.</span><button onClick={() => void save()} disabled={busy}>Save settings</button></div>
  </div></div>;
}

function KeyFields({ settings, change }: { settings: { api_key?: string; api_key_set: boolean; api_key_action?: string }; change(field: string, value: string): void }) {
  return <div className="key-fields"><label>API key action<select value={settings.api_key_action || "preserve"} onChange={(event) => change("api_key_action", event.target.value)}><option value="preserve">Preserve {settings.api_key_set ? "saved key" : "empty key"}</option><option value="replace">Replace key</option><option value="clear">Clear key</option></select></label>{settings.api_key_action === "replace" && <label>New API key<input type="password" value={settings.api_key || ""} onChange={(event) => change("api_key", event.target.value)} /></label>}</div>;
}

function ImportDialog({ onClose, onImported }: { onClose(): void; onImported(): Promise<void> }) {
  const [files, setFiles] = useState<File[]>([]);
  const [replace, setReplace] = useState(false);
  const [statuses, setStatuses] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  const totalSize = useMemo(() => files.reduce((sum, file) => sum + file.size, 0), [files]);

  const runImport = async () => {
    setBusy(true);
    for (const file of files) {
      setStatuses((value) => ({ ...value, [file.name]: "Importing…" }));
      try {
        const result = await importMarkdown(file, replace);
        setStatuses((value) => ({ ...value, [file.name]: `Imported ${result.chunks} chunks` }));
      } catch (error) {
        setStatuses((value) => ({ ...value, [file.name]: error instanceof Error ? error.message : "Import failed" }));
      }
    }
    await onImported();
    setBusy(false);
  };

  return <div className="modal-backdrop" role="presentation"><div className="modal" role="dialog" aria-modal="true" aria-label="Import Markdown">
    <div className="modal-header"><div><h2>Import Markdown</h2><p>Embedded PNG, JPEG, GIF, and WebP images are stored with their source sections.</p></div><button className="icon-button" onClick={onClose}>×</button></div>
    <label className="file-drop">
      <input type="file" accept=".md,.markdown,text/markdown" multiple onChange={(event) => setFiles(Array.from(event.target.files || []))} />
      <strong>Choose Markdown files</strong><span>Up to 100 MiB per upload</span>
    </label>
    {files.length > 0 && <div className="import-list">{files.map((file) => <div key={`${file.name}-${file.size}`}><span>{file.name} <small>{(file.size / 1024).toFixed(1)} KiB</small></span><span>{statuses[file.name] || "Ready"}</span></div>)}</div>}
    <label className="checkbox"><input type="checkbox" checked={replace} onChange={(event) => setReplace(event.target.checked)} /> Replace manuals with matching IDs</label>
    <div className="modal-footer"><span>{files.length ? `${files.length} file${files.length > 1 ? "s" : ""} · ${(totalSize / 1024 / 1024).toFixed(1)} MiB` : "No files selected"}</span><button onClick={() => void runImport()} disabled={!files.length || busy}>{busy ? "Importing…" : "Import"}</button></div>
  </div></div>;
}
