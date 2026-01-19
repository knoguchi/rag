package service

import (
	"context"
	"fmt"

	ragv1 "github.com/knoguchi/rag/gen/rag/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// DocumentServiceClient wraps the gRPC DocumentService client
// and implements the DocumentIngester interface
type DocumentServiceClient struct {
	conn   *grpc.ClientConn
	client ragv1.DocumentServiceClient
}

// NewDocumentServiceClient creates a new gRPC client to the RAG service's DocumentService
func NewDocumentServiceClient(ctx context.Context, ragServiceURL string) (*DocumentServiceClient, error) {
	conn, err := grpc.NewClient(
		ragServiceURL,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to RAG service at %s: %w", ragServiceURL, err)
	}

	client := ragv1.NewDocumentServiceClient(conn)

	return &DocumentServiceClient{
		conn:   conn,
		client: client,
	}, nil
}

// IngestDocument sends a document to the RAG service for ingestion
func (c *DocumentServiceClient) IngestDocument(ctx context.Context, req *ragv1.IngestDocumentRequest) (*ragv1.IngestDocumentResponse, error) {
	return c.client.IngestDocument(ctx, req)
}

// Close closes the gRPC connection
func (c *DocumentServiceClient) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}
