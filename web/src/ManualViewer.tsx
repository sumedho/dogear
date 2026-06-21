import { useEffect, useRef, useState } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { listDocumentChunks, loadDocumentChunks, searchManual } from "./api";
import type { DocumentChunk, DocumentInfo, SearchResult } from "./types";
import { useDialog } from "./useDialog";

export function ManualViewer({ manual, documentId, chunkId, onAsk, onClose }: { manual?: DocumentInfo; documentId: string; chunkId?: number; onAsk(chunk: DocumentChunk): void; onClose(): void }) {
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
    let active = true;
    setLoading(true); setHasMore(true); setError(""); loadingMoreRef.current = false;
    loadDocumentChunks(documentId, chunkId).then((page) => {
      if (!active) return;
      setChunks(page); setLoading(false); setHasMore(page.length === 50);
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
    let active = true;
    setSearching(true);
    const timer = window.setTimeout(() => searchManual(value, documentId)
      .then((items) => { if (active) setResults(items); })
      .catch((reason) => { if (active) setError(reason instanceof Error ? reason.message : "Search failed"); })
      .finally(() => { if (active) setSearching(false); }), 250);
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
