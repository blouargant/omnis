package embed

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNormalizeUnitLength(t *testing.T) {
	v := normalize([]float32{3, 4})
	got := math.Sqrt(float64(v[0]*v[0] + v[1]*v[1]))
	if math.Abs(got-1) > 1e-6 {
		t.Fatalf("expected unit length, got %v", got)
	}
}

func TestNormalizeZeroVector(t *testing.T) {
	v := normalize([]float32{0, 0, 0})
	for _, x := range v {
		if math.IsNaN(float64(x)) {
			t.Fatalf("zero vector produced NaN")
		}
	}
}

func TestEmbedsURL(t *testing.T) {
	cases := map[string]string{
		"https://api.openai.com/v1":  "https://api.openai.com/v1/embeddings",
		"https://api.openai.com/v1/": "https://api.openai.com/v1/embeddings",
		"http://localhost:11434":     "http://localhost:11434/v1/embeddings",
		"http://localhost:11434/":    "http://localhost:11434/v1/embeddings",
	}
	for in, want := range cases {
		if got := embedsURL(in); got != want {
			t.Errorf("embedsURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestOpenAIEmbedRoundTrip(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		var req openaiEmbedRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		resp := openaiEmbedResponse{}
		for i := range req.Input {
			resp.Data = append(resp.Data, struct {
				Embedding []float32 `json:"embedding"`
				Index     int       `json:"index"`
			}{Embedding: []float32{float32(i + 1), 0, 0}, Index: i})
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	emb, err := NewWithSelection(context.Background(), Selection{
		Provider: "openai_compat",
		Model:    "test-embed",
		BaseURL:  srv.URL,
		Dim:      3,
	})
	if err != nil {
		t.Fatal(err)
	}
	vecs, err := emb.Embed(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vecs) != 2 {
		t.Fatalf("want 2 vectors, got %d", len(vecs))
	}
	// Both vectors point along the x axis, so normalised they equal (1,0,0).
	for _, v := range vecs {
		if math.Abs(float64(v[0])-1) > 1e-6 {
			t.Errorf("expected normalised x=1, got %v", v)
		}
	}
}

func TestAnthropicUnsupported(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	_, err := NewWithSelection(context.Background(), Selection{Provider: "anthropic", APIKey: "x"})
	if err == nil {
		t.Fatal("expected ErrUnsupported for anthropic")
	}
}

func TestCacheAvoidsResecondCall(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())

	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var req openaiEmbedRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		resp := openaiEmbedResponse{}
		for i := range req.Input {
			resp.Data = append(resp.Data, struct {
				Embedding []float32 `json:"embedding"`
				Index     int       `json:"index"`
			}{Embedding: []float32{1, 0, 0}, Index: i})
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	emb, err := NewWithSelection(context.Background(), Selection{Provider: "openai_compat", Model: "m", BaseURL: srv.URL, Dim: 3})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := emb.Embed(context.Background(), []string{"same"}); err != nil {
		t.Fatal(err)
	}
	if _, err := emb.Embed(context.Background(), []string{"same"}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("expected 1 network call (second served from cache), got %d", calls)
	}
}
