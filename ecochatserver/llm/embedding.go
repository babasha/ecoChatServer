package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"sync"
	"time"
)

// ============================================================================
// Embedding Client — standalone client for vector embeddings
// Supports OpenAI (text-embedding-3-small) and Gemini (text-embedding-004)
// ============================================================================

// EmbeddingClient generates vector embeddings for text.
// Used by Director memory for semantic search (hybrid FTS + vector).
type EmbeddingClient struct {
	provider string // "openai" or "gemini"
	apiKey   string
	model    string
	dim      int
	client   *http.Client
}

var (
	globalEmbeddingClient *EmbeddingClient
	embeddingClientOnce   sync.Once
)

// InitEmbeddingClient creates a global embedding client from settings.
// Call with explicit params, or pass empty strings to load from DB.
func InitEmbeddingClient(provider, apiKey, model string) {
	embeddingClientOnce.Do(func() {
		if provider == "" || apiKey == "" {
			log.Println("[EMBEDDING] No provider/key configured, semantic search disabled")
			return
		}

		client, err := NewEmbeddingClient(provider, apiKey, model)
		if err != nil {
			log.Printf("[EMBEDDING] Failed to init: %v", err)
			return
		}

		globalEmbeddingClient = client
		log.Printf("[EMBEDDING] Initialized: provider=%s, model=%s, dim=%d", client.provider, client.model, client.dim)
	})
}

// GetEmbeddingClient returns the global embedding client (may be nil).
func GetEmbeddingClient() *EmbeddingClient {
	return globalEmbeddingClient
}

// NewEmbeddingClient creates a new embedding client.
func NewEmbeddingClient(provider, apiKey, model string) (*EmbeddingClient, error) {
	c := &EmbeddingClient{
		provider: provider,
		apiKey:   apiKey,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}

	switch provider {
	case "openai":
		if model == "" {
			model = "text-embedding-3-small"
		}
		c.model = model
		c.dim = 1536
		if model == "text-embedding-3-large" {
			c.dim = 3072
		}
	case "gemini":
		if model == "" {
			model = "text-embedding-004"
		}
		c.model = model
		c.dim = 768
	default:
		return nil, fmt.Errorf("unsupported embedding provider: %s (use 'openai' or 'gemini')", provider)
	}

	return c, nil
}

// Dimension returns the embedding vector dimension.
func (c *EmbeddingClient) Dimension() int {
	return c.dim
}

// Embed generates an embedding for a single text.
func (c *EmbeddingClient) Embed(ctx context.Context, text string) ([]float64, error) {
	if text == "" {
		return nil, fmt.Errorf("empty text")
	}

	switch c.provider {
	case "openai":
		return c.embedOpenAI(ctx, text)
	case "gemini":
		return c.embedGemini(ctx, text)
	default:
		return nil, fmt.Errorf("unsupported provider: %s", c.provider)
	}
}

// EmbedBatch generates embeddings for multiple texts in one API call.
func (c *EmbeddingClient) EmbedBatch(ctx context.Context, texts []string) ([][]float64, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	switch c.provider {
	case "openai":
		return c.embedBatchOpenAI(ctx, texts)
	case "gemini":
		// Gemini doesn't support batch embedding in one call, fall back to sequential
		results := make([][]float64, len(texts))
		for i, text := range texts {
			emb, err := c.embedGemini(ctx, text)
			if err != nil {
				return nil, fmt.Errorf("embed text %d: %w", i, err)
			}
			results[i] = emb
		}
		return results, nil
	default:
		return nil, fmt.Errorf("unsupported provider: %s", c.provider)
	}
}

// ── OpenAI Embedding API ─────────────────────────────────────────────────────

type openAIEmbeddingRequest struct {
	Input string `json:"input"`
	Model string `json:"model"`
}

type openAIEmbeddingBatchRequest struct {
	Input []string `json:"input"`
	Model string   `json:"model"`
}

type openAIEmbeddingResponse struct {
	Data []struct {
		Embedding []float64 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
	Usage struct {
		PromptTokens int `json:"prompt_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage"`
}

func (c *EmbeddingClient) embedOpenAI(ctx context.Context, text string) ([]float64, error) {
	body, _ := json.Marshal(openAIEmbeddingRequest{
		Input: text,
		Model: c.model,
	})

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.openai.com/v1/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai embedding request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("openai embedding error %d: %s", resp.StatusCode, string(respBody))
	}

	var result openAIEmbeddingResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode openai embedding: %w", err)
	}

	if len(result.Data) == 0 {
		return nil, fmt.Errorf("openai returned empty embedding")
	}

	return result.Data[0].Embedding, nil
}

func (c *EmbeddingClient) embedBatchOpenAI(ctx context.Context, texts []string) ([][]float64, error) {
	body, _ := json.Marshal(openAIEmbeddingBatchRequest{
		Input: texts,
		Model: c.model,
	})

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.openai.com/v1/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai batch embedding: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("openai batch embedding error %d: %s", resp.StatusCode, string(respBody))
	}

	var result openAIEmbeddingResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode openai batch embedding: %w", err)
	}

	embeddings := make([][]float64, len(texts))
	for _, d := range result.Data {
		if d.Index < len(embeddings) {
			embeddings[d.Index] = d.Embedding
		}
	}

	return embeddings, nil
}

// ── Gemini Embedding API ─────────────────────────────────────────────────────

type geminiEmbeddingRequest struct {
	Model   string             `json:"model"`
	Content geminiEmbedContent `json:"content"`
}

type geminiEmbedContent struct {
	Parts []geminiEmbedPart `json:"parts"`
}

type geminiEmbedPart struct {
	Text string `json:"text"`
}

type geminiEmbeddingResponse struct {
	Embedding struct {
		Values []float64 `json:"values"`
	} `json:"embedding"`
}

func (c *EmbeddingClient) embedGemini(ctx context.Context, text string) ([]float64, error) {
	body, _ := json.Marshal(geminiEmbeddingRequest{
		Model: "models/" + c.model,
		Content: geminiEmbedContent{
			Parts: []geminiEmbedPart{{Text: text}},
		},
	})

	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:embedContent?key=%s", c.model, c.apiKey)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gemini embedding request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("gemini embedding error %d: %s", resp.StatusCode, string(respBody))
	}

	var result geminiEmbeddingResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode gemini embedding: %w", err)
	}

	if len(result.Embedding.Values) == 0 {
		return nil, fmt.Errorf("gemini returned empty embedding")
	}

	return result.Embedding.Values, nil
}

// ============================================================================
// Vector math utilities
// ============================================================================

// CosineSimilarity computes cosine similarity between two vectors.
// Returns 0 if either vector is nil or empty.
func CosineSimilarity(a, b []float64) float64 {
	if len(a) == 0 || len(b) == 0 || len(a) != len(b) {
		return 0
	}

	var dot, normA, normB float64
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}

	if normA == 0 || normB == 0 {
		return 0
	}

	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}
