package service

import (
	"context"

	ragv1 "github.com/knoguchi/rag/gen/rag/v1"
	"github.com/knoguchi/rag/internal/auth"
	"github.com/knoguchi/rag/internal/ragcore"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// RAGService implements ragv1.RAGServiceServer as a thin adapter over the
// ragcore Engine: it resolves the tenant, maps tenant config to engine
// options, and converts between protos and engine types.
type RAGService struct {
	ragv1.UnimplementedRAGServiceServer

	engine *ragcore.Engine
}

// NewRAGService creates a new RAGService
func NewRAGService(engine *ragcore.Engine) *RAGService {
	return &RAGService{engine: engine}
}

// resolveTenant returns the authenticated tenant from the request context.
func resolveTenant(ctx context.Context, query string) (*auth.TenantInfo, error) {
	if query == "" {
		return nil, status.Error(codes.InvalidArgument, "query is required")
	}
	return auth.RequireTenant(ctx)
}

// buildOptions resolves engine options from tenant config and request options.
func buildOptions(tenant *auth.TenantInfo, opts *ragv1.QueryOptions, sessionID string) ragcore.Options {
	options := ragcore.Options{
		TopK:          tenant.Config.TopK,
		MinScore:      tenant.Config.MinScore,
		SystemPrompt:  tenant.Config.SystemPrompt,
		Temperature:   0.3,  // Low temperature for factual, deterministic RAG responses
		MaxTokens:     2048, // Default max tokens
		Model:         tenant.Config.LLMModel,
		RerankEnabled: tenant.Config.RerankerEnabled,
		HybridEnabled: tenant.Config.HybridEnabled,
		SessionID:     sessionID,
	}

	// Apply defaults if tenant config has zero values
	if options.TopK <= 0 {
		options.TopK = 4 // Fewer sources = more focused answers
	}
	if options.MinScore <= 0 {
		options.MinScore = 0.5 // Higher threshold = more relevant results only
	}
	if options.SystemPrompt == "" {
		options.SystemPrompt = defaultSystemPrompt
	}

	// Override with request options if provided
	if opts != nil {
		if opts.TopK > 0 {
			options.TopK = int(opts.TopK)
		}
		if opts.MinScore > 0 {
			options.MinScore = opts.MinScore
		}
		if opts.SystemPrompt != "" {
			options.SystemPrompt = opts.SystemPrompt
		}
		if opts.Temperature > 0 {
			options.Temperature = opts.Temperature
		}
		if opts.MaxTokens > 0 {
			options.MaxTokens = int(opts.MaxTokens)
		}
	}

	return options
}

func toProtoChunk(c ragcore.RetrievedChunk) *ragv1.RetrievedChunk {
	return &ragv1.RetrievedChunk{
		DocumentId: c.DocumentID,
		ChunkId:    c.ID,
		Content:    c.Content,
		Score:      c.Score,
		Source:     c.Source,
		Title:      c.Title,
		Metadata:   c.Metadata,
	}
}

// Query retrieves context and generates an LLM response
func (s *RAGService) Query(ctx context.Context, req *ragv1.QueryRequest) (*ragv1.QueryResponse, error) {
	tenant, err := resolveTenant(ctx, req.Query)
	if err != nil {
		return nil, err
	}

	options := buildOptions(tenant, req.Options, req.SessionId)

	result, err := s.engine.Query(ctx, tenant.ID.String(), req.Query, options)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "%v", err)
	}

	sources := make([]*ragv1.RetrievedChunk, len(result.Sources))
	for i, c := range result.Sources {
		sources[i] = toProtoChunk(c)
	}

	return &ragv1.QueryResponse{
		Answer:  result.Answer,
		Sources: sources,
		Metadata: &ragv1.QueryMetadata{
			RetrievalTimeMs:  result.Timings.Retrieval.Milliseconds(),
			GenerationTimeMs: result.Timings.Generation.Milliseconds(),
			TotalTimeMs:      result.Timings.Total.Milliseconds(),
			ChunksRetrieved:  int32(len(sources)),
			Model:            result.Model,
			PromptTokens:     0, // TODO: Implement token counting
			CompletionTokens: 0, // TODO: Implement token counting
		},
	}, nil
}

// QueryStream streams the LLM response for interactive use
func (s *RAGService) QueryStream(req *ragv1.QueryRequest, stream grpc.ServerStreamingServer[ragv1.QueryStreamResponse]) error {
	ctx := stream.Context()

	tenant, err := resolveTenant(ctx, req.Query)
	if err != nil {
		return err
	}

	options := buildOptions(tenant, req.Options, req.SessionId)

	err = s.engine.QueryStream(ctx, tenant.ID.String(), req.Query, options, func(ev ragcore.StreamEvent) error {
		switch {
		case ev.Source != nil:
			return stream.Send(&ragv1.QueryStreamResponse{
				Event: &ragv1.QueryStreamResponse_Source{Source: toProtoChunk(*ev.Source)},
			})
		case ev.Err != nil:
			return stream.Send(&ragv1.QueryStreamResponse{
				Event: &ragv1.QueryStreamResponse_Error{
					Error: &ragv1.StreamError{
						Code:    "generation_error",
						Message: ev.Err.Error(),
					},
				},
			})
		case ev.Meta != nil:
			return stream.Send(&ragv1.QueryStreamResponse{
				Event: &ragv1.QueryStreamResponse_Metadata{
					Metadata: &ragv1.QueryMetadata{
						RetrievalTimeMs:  ev.Meta.Timings.Retrieval.Milliseconds(),
						GenerationTimeMs: ev.Meta.Timings.Generation.Milliseconds(),
						TotalTimeMs:      ev.Meta.Timings.Total.Milliseconds(),
						ChunksRetrieved:  int32(len(ev.Meta.Sources)),
						Model:            ev.Meta.Model,
						PromptTokens:     0,
						CompletionTokens: 0,
					},
				},
			})
		default:
			return stream.Send(&ragv1.QueryStreamResponse{
				Event: &ragv1.QueryStreamResponse_Token{Token: ev.Token},
			})
		}
	})
	if err != nil {
		if _, ok := status.FromError(err); ok {
			return err
		}
		return status.Errorf(codes.Internal, "%v", err)
	}
	return nil
}

// Retrieve only retrieves relevant chunks without LLM generation
func (s *RAGService) Retrieve(ctx context.Context, req *ragv1.RetrieveRequest) (*ragv1.RetrieveResponse, error) {
	tenant, err := resolveTenant(ctx, req.Query)
	if err != nil {
		return nil, err
	}

	options := ragcore.Options{
		TopK:          tenant.Config.TopK,
		MinScore:      tenant.Config.MinScore,
		HybridEnabled: tenant.Config.HybridEnabled,
	}
	var documentIDs []string
	if req.Options != nil {
		if req.Options.TopK > 0 {
			options.TopK = int(req.Options.TopK)
		}
		if req.Options.MinScore > 0 {
			options.MinScore = req.Options.MinScore
		}
		documentIDs = req.Options.DocumentIds
	}

	result, err := s.engine.Retrieve(ctx, tenant.ID.String(), req.Query, options, documentIDs)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "%v", err)
	}

	chunks := make([]*ragv1.RetrievedChunk, len(result.Chunks))
	for i, c := range result.Chunks {
		chunks[i] = toProtoChunk(c)
	}

	return &ragv1.RetrieveResponse{
		Chunks: chunks,
		Metadata: &ragv1.RetrieveMetadata{
			RetrievalTimeMs:     result.Retrieval.Milliseconds(),
			ChunksRetrieved:     int32(len(chunks)),
			TotalChunksSearched: 0, // TODO: Get from vector store if available
		},
	}, nil
}
