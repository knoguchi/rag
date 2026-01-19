/**
 * RAG SDK Types
 */

export interface RAGClientConfig {
  /** Base URL of the RAG service (e.g., http://localhost:8080) */
  baseUrl: string;
  /** Tenant ID for multi-tenant access */
  tenantId: string;
  /** Optional API key for authentication */
  apiKey?: string;
  /** Request timeout in milliseconds (default: 30000) */
  timeout?: number;
}

export interface QueryOptions {
  /** Number of chunks to retrieve (default: 5) */
  topK?: number;
  /** Minimum similarity score threshold (0.0 - 1.0) */
  minScore?: number;
  /** Custom system prompt to override tenant default */
  systemPrompt?: string;
  /** Enable streaming response */
  stream?: boolean;
}

export interface RetrievedChunk {
  /** Document ID the chunk belongs to */
  documentId: string;
  /** Chunk text content */
  content: string;
  /** Similarity score (0.0 - 1.0) */
  score: number;
  /** Additional metadata */
  metadata: Record<string, string>;
}

export interface QueryResponse {
  /** Generated answer from the LLM */
  answer: string;
  /** Retrieved source chunks used to generate the answer */
  sources: RetrievedChunk[];
  /** Query metadata */
  metadata?: QueryMetadata;
}

export interface QueryMetadata {
  /** Time taken to retrieve chunks (ms) */
  retrievalTimeMs?: number;
  /** Time taken to generate response (ms) */
  generationTimeMs?: number;
  /** Total processing time (ms) */
  totalTimeMs?: number;
  /** Model used for generation */
  model?: string;
}

export interface RetrieveResponse {
  /** Retrieved chunks without LLM generation */
  chunks: RetrievedChunk[];
}

export interface Document {
  /** Document ID */
  id: string;
  /** Tenant ID */
  tenantId: string;
  /** Source URL or identifier */
  source: string;
  /** Document title */
  title?: string;
  /** Number of chunks */
  chunkCount: number;
  /** Document status */
  status: DocumentStatus;
  /** Creation timestamp */
  createdAt: string;
  /** Last update timestamp */
  updatedAt: string;
}

export type DocumentStatus =
  | 'pending'
  | 'processing'
  | 'ready'
  | 'failed';

export interface IngestOptions {
  /** Document title */
  title?: string;
  /** Additional metadata */
  metadata?: Record<string, string>;
}

export interface IngestResponse {
  /** Created document ID */
  documentId: string;
  /** Document status */
  status: DocumentStatus;
  /** Number of chunks created */
  chunkCount?: number;
}

export interface StreamEvent {
  /** Event type */
  type: 'token' | 'source' | 'metadata' | 'done' | 'error';
  /** Token content (for type='token') */
  token?: string;
  /** Source chunk (for type='source') */
  source?: RetrievedChunk;
  /** Metadata (for type='metadata') */
  metadata?: QueryMetadata;
  /** Error message (for type='error') */
  error?: string;
}

export type StreamCallback = (event: StreamEvent) => void;
