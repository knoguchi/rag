// Command ragreindex rebuilds a tenant's vector collection as a hybrid
// (dense + BM25 sparse) collection from the chunks stored in Postgres, then
// flips the tenant's hybrid_enabled flag on. Use it to migrate tenants
// created before hybrid search existed — no re-crawl needed.
//
// Usage:
//
//	ragreindex --tenant <uuid>   reindex one tenant
//	ragreindex --all             reindex every tenant
//	--contextual                 also generate situating context for chunks
//	                             that lack one (one LLM call per chunk)
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/knoguchi/rag/internal/config"
	"github.com/knoguchi/rag/internal/ids"
	"github.com/knoguchi/rag/internal/ragcore"
	"github.com/knoguchi/rag/internal/ragcore/embedder"
	"github.com/knoguchi/rag/internal/ragcore/llm"
	"github.com/knoguchi/rag/internal/ragcore/sparse"
	"github.com/knoguchi/rag/internal/ragcore/vectorstore"
	"github.com/knoguchi/rag/internal/repository"
	"github.com/knoguchi/rag/internal/repository/postgres"
)

func main() {
	tenantFlag := flag.String("tenant", "", "tenant UUID to reindex")
	allFlag := flag.Bool("all", false, "reindex all tenants")
	contextualFlag := flag.Bool("contextual", false, "generate situating context for chunks that lack one")
	flag.Parse()

	if (*tenantFlag == "") == !*allFlag {
		fmt.Fprintln(os.Stderr, "usage: ragreindex --tenant <uuid> | --all")
		os.Exit(2)
	}

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))

	if err := run(*tenantFlag, *allFlag, *contextualFlag); err != nil {
		slog.Error("reindex failed", "error", err)
		os.Exit(1)
	}
}

func run(tenantID string, all, contextual bool) error {
	ctx := context.Background()

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	db, err := postgres.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}
	defer db.Close()

	store, err := vectorstore.NewQdrantStore(ctx, cfg.QdrantGRPCURL)
	if err != nil {
		return fmt.Errorf("failed to connect to Qdrant: %w", err)
	}
	defer store.Close()

	emb := embedder.NewOllamaEmbedder(embedder.OllamaConfig{
		BaseURL: cfg.OllamaURL,
		Model:   cfg.OllamaEmbeddingModel,
	})
	llmClient := llm.NewOllamaClient(
		llm.WithBaseURL(cfg.OllamaURL),
		llm.WithModel(cfg.OllamaLLMModel),
	)

	engine := ragcore.New(emb, llmClient, store,
		ragcore.WithSparseVectorizer(sparse.New()),
	)
	defer engine.Close()

	tenantRepo := postgres.NewTenantRepo(db)
	docRepo := postgres.NewDocumentRepo(db)

	var tenants []*repository.Tenant
	if all {
		list, _, err := tenantRepo.List(ctx, 10000, 0)
		if err != nil {
			return fmt.Errorf("failed to list tenants: %w", err)
		}
		tenants = list
	} else {
		id, err := ids.Parse(tenantID)
		if err != nil {
			return fmt.Errorf("invalid tenant id: %w", err)
		}
		tenant, err := tenantRepo.GetByID(ctx, id)
		if err != nil {
			return fmt.Errorf("failed to load tenant: %w", err)
		}
		tenants = []*repository.Tenant{tenant}
	}

	for _, tenant := range tenants {
		if err := reindexTenant(ctx, engine, tenantRepo, docRepo, tenant, contextual); err != nil {
			return fmt.Errorf("tenant %s: %w", tenant.ID, err)
		}
	}

	return nil
}

func reindexTenant(
	ctx context.Context,
	engine *ragcore.Engine,
	tenantRepo repository.TenantRepository,
	docRepo repository.DocumentRepository,
	tenant *repository.Tenant,
	contextual bool,
) error {
	ns := tenant.ID.UUIDString()
	slog.Info("reindexing tenant", "tenant_id", ns, "name", tenant.Name)

	// Recreate the collection as hybrid
	exists, err := engine.NamespaceExists(ctx, ns)
	if err != nil {
		return err
	}
	if exists {
		if err := engine.DeleteNamespace(ctx, ns); err != nil {
			return fmt.Errorf("failed to delete collection: %w", err)
		}
	}
	if err := engine.CreateNamespace(ctx, ns); err != nil {
		return fmt.Errorf("failed to create hybrid collection: %w", err)
	}

	// Re-embed every READY document's chunks from Postgres
	const pageSize = 100
	for offset := 0; ; offset += pageSize {
		docs, _, err := docRepo.List(ctx, tenant.ID, "READY", pageSize, offset)
		if err != nil {
			return fmt.Errorf("failed to list documents: %w", err)
		}
		if len(docs) == 0 {
			break
		}

		for _, doc := range docs {
			if err := reindexDocument(ctx, engine, docRepo, tenant, doc, contextual); err != nil {
				return fmt.Errorf("document %s: %w", doc.ID, err)
			}
		}

		if len(docs) < pageSize {
			break
		}
	}

	// Flip the tenant to hybrid retrieval
	tenant.Config.HybridEnabled = true
	if contextual {
		tenant.Config.ContextualRetrievalEnabled = true
	}
	if err := tenantRepo.Update(ctx, tenant); err != nil {
		return fmt.Errorf("failed to enable hybrid on tenant: %w", err)
	}

	slog.Info("tenant reindexed", "tenant_id", ns)
	return nil
}

func reindexDocument(
	ctx context.Context,
	engine *ragcore.Engine,
	docRepo repository.DocumentRepository,
	tenant *repository.Tenant,
	doc *repository.Document,
	contextual bool,
) error {
	const chunkPage = 500
	var chunks []ragcore.IngestedChunk

	for offset := 0; ; offset += chunkPage {
		stored, err := docRepo.GetChunks(ctx, tenant.ID, doc.ID, chunkPage, offset)
		if err != nil {
			return fmt.Errorf("failed to load chunks: %w", err)
		}
		for _, c := range stored {
			chunks = append(chunks, ragcore.IngestedChunk{
				ID:       c.ID.UUIDString(),
				Index:    c.ChunkIndex,
				Content:  c.Content,
				Metadata: c.Metadata,
			})
		}
		if len(stored) < chunkPage {
			break
		}
	}

	if contextual {
		// The original document isn't stored; approximate it by
		// concatenating the chunks (they cover the document in order).
		var docText strings.Builder
		for _, c := range chunks {
			if docText.Len() > 4000 {
				break
			}
			docText.WriteString(c.Content)
			docText.WriteString("\n")
		}
		var missing []ragcore.IngestedChunk
		idx := make([]int, 0, len(chunks))
		for i, c := range chunks {
			if c.Metadata["context"] == "" {
				missing = append(missing, c)
				idx = append(idx, i)
			}
		}
		engine.Contextualize(ctx, docText.String(), tenant.Config.LLMModel, missing)
		for j, i := range idx {
			chunks[i] = missing[j]
		}
	}

	defaults := map[string]string{
		"source": doc.Source,
		"title":  doc.Title,
	}
	if err := engine.IndexChunks(ctx, tenant.ID.UUIDString(), doc.ID.UUIDString(), chunks, defaults, true); err != nil {
		return err
	}

	slog.Info("document reindexed", "document_id", doc.ID, "chunks", len(chunks))
	return nil
}
