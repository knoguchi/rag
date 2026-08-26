/**
 * RAG Client - TypeScript SDK for RAG as a Service
 */

import type {
  RAGClientConfig,
  QueryOptions,
  QueryResponse,
  RetrieveResponse,
  Document,
  IngestOptions,
  IngestResponse,
  StreamEvent,
  StreamCallback,
} from './types.js';

export class RAGClient {
  private baseUrl: string;
  private apiKey: string;
  private timeout: number;

  constructor(config: RAGClientConfig) {
    this.baseUrl = config.baseUrl.replace(/\/$/, ''); // Remove trailing slash
    this.apiKey = config.apiKey;
    this.timeout = config.timeout ?? 30000;
    if (!this.apiKey) {
      throw new Error('RAGClient requires an apiKey');
    }
  }

  /**
   * Query the RAG service with a question
   */
  async query(question: string, options?: QueryOptions): Promise<QueryResponse> {
    const response = await this.fetch('/v1/query', {
      method: 'POST',
      body: JSON.stringify({
        query: question,
        session_id: options?.sessionId,
        options: options ? {
          top_k: options.topK,
          min_score: options.minScore,
          system_prompt: options.systemPrompt,
        } : undefined,
      }),
    });

    const data = await response.json();

    return {
      answer: data.answer,
      sources: (data.sources || []).map(this.mapChunk),
      metadata: data.metadata ? {
        retrievalTimeMs: data.metadata.retrieval_time_ms,
        generationTimeMs: data.metadata.generation_time_ms,
        totalTimeMs: data.metadata.total_time_ms,
        model: data.metadata.model,
      } : undefined,
    };
  }

  /**
   * Query with streaming response
   */
  async queryStream(
    question: string,
    callback: StreamCallback,
    options?: QueryOptions
  ): Promise<void> {
    const response = await this.fetch('/v1/query/stream', {
      method: 'POST',
      headers: {
        'Accept': 'text/event-stream',
      },
      body: JSON.stringify({
        query: question,
        session_id: options?.sessionId,
        options: options ? {
          top_k: options.topK,
          min_score: options.minScore,
          system_prompt: options.systemPrompt,
          stream: true,
        } : { stream: true },
      }),
    });

    if (!response.body) {
      throw new Error('Streaming not supported');
    }

    const reader = response.body.getReader();
    const decoder = new TextDecoder();
    let buffer = '';

    try {
      while (true) {
        const { done, value } = await reader.read();
        if (done) break;

        buffer += decoder.decode(value, { stream: true });
        const lines = buffer.split('\n');
        buffer = lines.pop() || '';

        for (const rawLine of lines) {
          const line = rawLine.trim();
          if (line !== '') {
            const data = line.startsWith('data:') ? line.slice(5).trim() : line;
            if (data === '[DONE]') {
              callback({ type: 'done' });
              return;
            }

            try {
              // grpc-gateway wraps each stream message as {"result": {...}}
              const parsed = JSON.parse(data);
              const event = parsed.result ?? parsed;
              if (event.error) {
                callback({ type: 'error', error: event.error.message ?? String(event.error) });
              } else if (event.token) {
                callback({ type: 'token', token: event.token });
              } else if (event.source) {
                callback({ type: 'source', source: this.mapChunk(event.source) });
              } else if (event.metadata) {
                callback({
                  type: 'metadata',
                  metadata: {
                    retrievalTimeMs: event.metadata.retrieval_time_ms,
                    generationTimeMs: event.metadata.generation_time_ms,
                    totalTimeMs: event.metadata.total_time_ms,
                    model: event.metadata.model,
                  },
                });
              }
            } catch {
              // Ignore parse errors for malformed events
            }
          }
        }
      }
    } finally {
      reader.releaseLock();
    }

    callback({ type: 'done' });
  }

  /**
   * Retrieve relevant chunks without LLM generation
   */
  async retrieve(question: string, options?: QueryOptions): Promise<RetrieveResponse> {
    const response = await this.fetch('/v1/retrieve', {
      method: 'POST',
      body: JSON.stringify({
        query: question,
        options: options ? {
          top_k: options.topK,
          min_score: options.minScore,
        } : undefined,
      }),
    });

    const data = await response.json();

    return {
      chunks: (data.chunks || []).map(this.mapChunk),
    };
  }

  /**
   * Ingest a document from text content
   */
  async ingest(content: string, source: string, options?: IngestOptions): Promise<IngestResponse> {
    const response = await this.fetch('/v1/documents/ingest', {
      method: 'POST',
      body: JSON.stringify({
        content,
        source,
        title: options?.title,
        metadata: options?.metadata,
      }),
    });

    const data = await response.json();

    return {
      documentId: data.document_id || data.documentId,
      status: data.status?.toLowerCase() || 'pending',
      chunkCount: data.chunk_count,
    };
  }

  /**
   * Ingest a document from URL
   */
  async ingestUrl(url: string, options?: IngestOptions): Promise<IngestResponse> {
    const response = await this.fetch('/v1/documents/ingest-url', {
      method: 'POST',
      body: JSON.stringify({
        url,
        title: options?.title,
        metadata: options?.metadata,
      }),
    });

    const data = await response.json();

    return {
      documentId: data.document_id || data.documentId,
      status: data.status?.toLowerCase() || 'pending',
      chunkCount: data.chunk_count,
    };
  }

  /**
   * Get a document by ID
   */
  async getDocument(documentId: string): Promise<Document> {
    const response = await this.fetch(`/v1/documents/${documentId}`);
    const data = await response.json();
    return this.mapDocument(data);
  }

  /**
   * List all documents for the tenant
   */
  async listDocuments(): Promise<Document[]> {
    const response = await this.fetch(`/v1/documents`);
    const data = await response.json();
    return (data.documents || []).map(this.mapDocument);
  }

  /**
   * Delete a document
   */
  async deleteDocument(documentId: string): Promise<void> {
    await this.fetch(`/v1/documents/${documentId}`, {
      method: 'DELETE',
    });
  }

  // Private helper methods

  private async fetch(path: string, options?: RequestInit): Promise<Response> {
    const url = `${this.baseUrl}${path}`;
    const headers: Record<string, string> = {
      'Content-Type': 'application/json',
      ...((options?.headers as Record<string, string>) || {}),
    };

    headers['X-API-Key'] = this.apiKey;

    const controller = new AbortController();
    const timeoutId = setTimeout(() => controller.abort(), this.timeout);

    try {
      const response = await fetch(url, {
        ...options,
        headers,
        signal: controller.signal,
      });

      if (!response.ok) {
        const error = await response.text();
        throw new Error(`RAG API error (${response.status}): ${error}`);
      }

      return response;
    } finally {
      clearTimeout(timeoutId);
    }
  }

  private mapChunk = (chunk: any) => ({
    documentId: chunk.document_id || chunk.documentId,
    content: chunk.content,
    score: chunk.score,
    metadata: chunk.metadata || {},
  });

  private mapDocument = (doc: any): Document => ({
    id: doc.id,
    tenantId: doc.tenant_id || doc.tenantId,
    source: doc.source,
    title: doc.title,
    chunkCount: doc.chunk_count || doc.chunkCount,
    status: (doc.status || 'pending').toLowerCase(),
    createdAt: doc.created_at || doc.createdAt,
    updatedAt: doc.updated_at || doc.updatedAt,
  });
}
