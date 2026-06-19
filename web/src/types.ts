export interface DocumentInfo {
  id: string;
  title: string;
  brand?: string;
  model?: string;
  version?: string;
  source_path: string;
  tags: string[];
  chunk_count: number;
  page_count: number;
}

export interface ImageRef {
  id: number;
  alt: string;
  media_type: string;
}

export interface DisplayImage extends ImageRef {
  source: SourceRef;
}

export interface SourceRef {
  chunk_id: number;
  label: string;
  document_id: string;
  title: string;
  brand?: string;
  model?: string;
  heading_path: string;
  page_number: number | null;
  start_line: number;
  end_line: number;
  score: number;
}

export interface ContextBlock {
  source: SourceRef;
  text: string;
  images?: ImageRef[];
}

export interface RetrievalResult {
  query: string;
  mode?: "fts" | "hybrid" | string;
  fallback_reason?: string;
  blocks: ContextBlock[];
}

export interface SearchResult {
  chunk_id: number;
  document_id: string;
  title: string;
  heading_path: string;
  page_number: number | null;
  start_line: number;
  end_line: number;
  snippet: string;
  score: number;
}

export interface AskResult {
  answer: string;
  model: string;
  provider_url: string;
  sources: SourceRef[];
  retrieval: RetrievalResult;
  images?: DisplayImage[];
}

export interface ChatMessage {
  id: string;
  role: "user" | "assistant";
  content: string;
  status?: "streaming" | "done" | "error" | "cancelled";
  error?: string;
  sources?: SourceRef[];
  retrieval?: RetrievalResult;
  images?: DisplayImage[];
}

export interface Chat {
  id: string;
  title: string;
  documentId: string;
  draft: string;
  messages: ChatMessage[];
  createdAt: number;
  updatedAt: number;
}

export interface DocumentImportWarning { code: string; message: string; line?: number; }
export interface IndexCoverage { indexed: number; total: number; complete: boolean; }
export interface VectorCoverage extends IndexCoverage { configured: boolean; stale: boolean; model?: string; updated_at?: string; }
export interface DocumentHealth {
  document_id: string;
  chunk_count: number;
  image_count: number;
  fts: IndexCoverage;
  vectors: VectorCoverage;
  warnings: DocumentImportWarning[];
}

export interface DocumentChunk {
  id: number; document_id: string; ordinal: number; heading_path: string; heading_level: number;
  page_number: number | null; start_line: number; end_line: number; text: string; images?: ImageRef[];
}

export interface ProviderSettings { base_url: string; model: string; timeout: string; api_key?: string; api_key_set: boolean; api_key_action?: string; }
export interface EmbeddingSettings extends ProviderSettings { dimensions: number; batch_size: number; query_instruction: string; }
export interface Settings { provider: ProviderSettings; embedding: EmbeddingSettings; environment_overrides: string[]; }
export interface EmbeddingIndexStatus { configured: boolean; complete: boolean; stale: boolean; model: string; dimensions: number; indexed: number; total: number; updated_at?: string; }
