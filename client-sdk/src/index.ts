/**
 * RAG SDK - TypeScript client for RAG as a Service
 *
 * @example
 * ```typescript
 * import { RAGClient, ChatWidget } from 'rag-service-sdk';
 *
 * // Option 1: Use the API client directly
 * const client = new RAGClient({
 *   baseUrl: 'http://localhost:8080',
 *   tenantId: 'your-tenant-id',
 * });
 * const response = await client.query('How do I create a database?');
 *
 * // Option 2: Drop-in chat widget
 * new ChatWidget({
 *   baseUrl: 'http://localhost:8080',
 *   tenantId: 'your-tenant-id',
 * });
 * ```
 */

export { RAGClient } from './client.js';
export { ChatWidget } from './widget.js';
export type {
  RAGClientConfig,
  QueryOptions,
  QueryResponse,
  QueryMetadata,
  RetrieveResponse,
  RetrievedChunk,
  Document,
  DocumentStatus,
  IngestOptions,
  IngestResponse,
  StreamEvent,
  StreamCallback,
} from './types.js';
export type { ChatWidgetConfig } from './widget.js';
