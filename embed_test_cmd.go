// embed_test_cmd.go — `yoke embed-test` subcommand. A self-contained probe
// that resolves the SAME embedder the server uses (models.json embed_model_ref
// / YOKE_EMBED_*), runs a few real embeddings, and reports the model, the
// observed dimension, and a sanity check on cosine similarity so you can
// confirm semantic recall is actually working end to end.
//
// Usage:
//
//	yoke embed-test                 # uses built-in sample sentences
//	yoke embed-test "your text"     # embeds your text + prints the vector head
package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/blouargant/yoke/agent"
)

func runEmbedTest(ctx context.Context, opts options, args []string) error {
	runtime, err := agent.ResolveRuntimeSettings(agent.Options{
		AppName:          opts.appName,
		ConfigPath:       opts.configPath,
		ConfigPathStrict: opts.configPath != "",
	})
	if err != nil {
		return err
	}

	fmt.Printf("embed_model_ref: %q\n", runtime.EmbedModelRef)
	if ref := strings.TrimSpace(runtime.EmbedModelRef); ref != "" {
		if m, ok := runtime.Models[strings.ToLower(ref)]; ok {
			fmt.Printf("  → provider=%q model=%q base_url=%q dim=%d api_key=%s\n",
				m.Provider, m.Model, m.BaseURL, m.Dim, maskSecret(m.APIKey))
		} else {
			fmt.Printf("  ⚠ %q is not present in models.json — semantic recall is OFF\n", ref)
		}
	}

	emb, err := agent.ResolveEmbedder(ctx, runtime)
	if err != nil {
		return fmt.Errorf("embedder failed to build: %w", err)
	}
	if emb == nil {
		return fmt.Errorf("no embedder configured — set embed_model_ref in models.json (and mark the model \"embedding\": true) or the YOKE_EMBED_* env; semantic recall is disabled")
	}

	// Custom text mode: embed it and show the vector head.
	if custom := strings.TrimSpace(strings.Join(args, " ")); custom != "" {
		vecs, err := emb.Embed(ctx, []string{custom})
		if err != nil {
			return fmt.Errorf("embed call failed (check base_url/api_key/model): %w", err)
		}
		v := vecs[0]
		fmt.Printf("\nOK — embedded %d chars\n  model=%q dim=%d\n  vector[0:8]=%v\n",
			len(custom), emb.Model(), len(v), head(v, 8))
		return nil
	}

	// Default: a relatedness sanity check.
	texts := []string{
		"how do I restart a crashed kubernetes pod",
		"diagnosing a failing kubernetes deployment",
		"my favourite recipe for chocolate cake",
	}
	vecs, err := emb.Embed(ctx, texts)
	if err != nil {
		return fmt.Errorf("embed call failed (check base_url/api_key/model): %w", err)
	}

	related := cosine(vecs[0], vecs[1])
	unrelated := cosine(vecs[0], vecs[2])
	selfSim := cosine(vecs[0], vecs[0])

	fmt.Printf("\nOK — embedder responded\n")
	fmt.Printf("  model=%q  dim=%d\n", emb.Model(), len(vecs[0]))
	fmt.Printf("  cosine(self)            = %.4f  (expect ≈ 1.0)\n", selfSim)
	fmt.Printf("  cosine(related pair)    = %.4f\n", related)
	fmt.Printf("  cosine(unrelated pair)  = %.4f\n", unrelated)
	if related > unrelated {
		fmt.Printf("\n✓ related > unrelated — embeddings are semantically meaningful.\n")
	} else {
		fmt.Printf("\n⚠ related is NOT greater than unrelated — the endpoint responded but the\n" +
			"  vectors look off (wrong model? a chat model instead of an embedding model?).\n")
	}
	return nil
}

func cosine(a, b []float32) float64 {
	// Vectors are L2-normalised by core/embed, so the dot product is the cosine.
	var dot float64
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		dot += float64(a[i]) * float64(b[i])
	}
	return dot
}

func head(v []float32, n int) []float32 {
	if len(v) < n {
		return v
	}
	return v[:n]
}

func maskSecret(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "(empty)"
	}
	if len(s) <= 6 {
		return "***"
	}
	return s[:3] + "…" + s[len(s)-2:]
}
