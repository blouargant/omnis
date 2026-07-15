package tools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestNewSerperToolsNilWithoutKey locks in the contract that an empty key mounts
// no tool — this is what keeps a multi-provider tool list (serper + ddg) from
// registering two tools named "WebSearch", which ADK rejects as a duplicate.
func TestNewSerperToolsNilWithoutKey(t *testing.T) {
	if ts := NewSerperTools(""); ts != nil {
		t.Fatalf("expected nil tools for empty key, got %d", len(ts))
	}
	if ts := NewSerperTools("   "); ts != nil {
		t.Fatalf("expected nil tools for blank key, got %d", len(ts))
	}
	ts := NewSerperTools("k")
	if len(ts) != 1 || ts[0].Name() != "WebSearch" {
		t.Fatalf("expected one WebSearch tool, got %+v", ts)
	}
}

// TestRunSerperSearchRequestAndParse pins the wire contract: POST to the
// endpoint with the X-API-KEY header and a {q,num} JSON body, and parsing of the
// organic[] results into the markdown list format.
func TestRunSerperSearchRequestAndParse(t *testing.T) {
	var gotMethod, gotKey, gotContentType string
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotKey = r.Header.Get("X-API-KEY")
		gotContentType = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"organic":[
			{"title":"First","link":"https://a.example","snippet":"alpha"},
			{"title":"Second","link":"https://b.example","snippet":"beta"},
			{"title":"","link":"https://skip.example","snippet":"no title, skipped"}
		]}`)
	}))
	defer srv.Close()

	orig := serperEndpoint
	serperEndpoint = srv.URL
	defer func() { serperEndpoint = orig }()

	out, err := runSerperSearch(context.Background(), "secret-key", DDGIn{Query: "go adk", MaxResults: 5})
	if err != nil {
		t.Fatalf("runSerperSearch: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotKey != "secret-key" {
		t.Errorf("X-API-KEY = %q, want secret-key", gotKey)
	}
	if !strings.HasPrefix(gotContentType, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", gotContentType)
	}
	if gotBody["q"] != "go adk" {
		t.Errorf("body q = %v, want %q", gotBody["q"], "go adk")
	}
	// JSON numbers decode as float64.
	if n, _ := gotBody["num"].(float64); n != 5 {
		t.Errorf("body num = %v, want 5", gotBody["num"])
	}

	want := "[First](https://a.example) — alpha\n[Second](https://b.example) — beta"
	if out != want {
		t.Errorf("output mismatch:\n got: %q\nwant: %q", out, want)
	}
}

// TestRunSerperSearchClampsAndDefaults verifies the max_results default (5) and
// upper clamp (10) match the tool's documented contract.
func TestRunSerperSearchClampsAndDefaults(t *testing.T) {
	cases := []struct{ in, want int }{
		{0, 5}, {-3, 5}, {7, 7}, {10, 10}, {50, 10},
	}
	for _, c := range cases {
		var gotNum float64
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, &body)
			gotNum, _ = body["num"].(float64)
			io.WriteString(w, `{"organic":[]}`)
		}))
		orig := serperEndpoint
		serperEndpoint = srv.URL
		if _, err := runSerperSearch(context.Background(), "k", DDGIn{Query: "x", MaxResults: c.in}); err != nil {
			t.Fatalf("runSerperSearch: %v", err)
		}
		serperEndpoint = orig
		srv.Close()
		if int(gotNum) != c.want {
			t.Errorf("MaxResults=%d → num=%v, want %d", c.in, gotNum, c.want)
		}
	}
}

// TestTestSerperKey verifies the connectivity probe behind the settings "Test"
// button: nil on a 200, a descriptive error on a non-200 (e.g. a rejected key),
// and a clear error for an empty key without any network call.
func TestTestSerperKey(t *testing.T) {
	if err := TestSerperKey(context.Background(), ""); err == nil {
		t.Fatal("empty key: expected error, got nil")
	}

	// Success: 200 + a valid key echoed in the header.
	okSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-KEY") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		io.WriteString(w, `{"organic":[]}`)
	}))
	defer okSrv.Close()
	orig := serperEndpoint
	serperEndpoint = okSrv.URL
	if err := TestSerperKey(context.Background(), "good-key"); err != nil {
		t.Errorf("valid key: expected nil, got %v", err)
	}
	serperEndpoint = orig

	// Failure: a 401 with a body is surfaced as an error including the status.
	badSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"message":"Unauthorized"}`)
	}))
	defer badSrv.Close()
	serperEndpoint = badSrv.URL
	defer func() { serperEndpoint = orig }()
	err := TestSerperKey(context.Background(), "bad-key")
	if err == nil {
		t.Fatal("rejected key: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error should mention the status code, got %q", err.Error())
	}
}

// TestRunSerperSearchNoResults verifies the empty-organic path.
func TestRunSerperSearchNoResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"organic":[]}`)
	}))
	defer srv.Close()
	orig := serperEndpoint
	serperEndpoint = srv.URL
	defer func() { serperEndpoint = orig }()

	out, err := runSerperSearch(context.Background(), "k", DDGIn{Query: "nothing"})
	if err != nil {
		t.Fatalf("runSerperSearch: %v", err)
	}
	if out != "(no results)" {
		t.Errorf("output = %q, want %q", out, "(no results)")
	}
}
