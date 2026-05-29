package embed

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"math"
	"os"
	"path/filepath"

	"github.com/blouargant/yoke/internal/paths"
)

// cachingEmbedder wraps a base Embedder with a content-addressed on-disk cache
// so unchanged text is never re-embedded — embeddings are paid network calls.
// Cache files live under $YOKE_HOME/index/embed_cache/<aa>/<sha256>.vec and
// store the L2-normalised vector as little-endian float32.
type cachingEmbedder struct {
	base Embedder
	dir  string
}

func newCachingEmbedder(base Embedder) *cachingEmbedder {
	return &cachingEmbedder{base: base, dir: filepath.Join(paths.IndexDir(), "embed_cache")}
}

func (c *cachingEmbedder) Model() string { return c.base.Model() }
func (c *cachingEmbedder) Dim() int      { return c.base.Dim() }

func (c *cachingEmbedder) key(text string) string {
	h := sha256.Sum256([]byte(c.base.Model() + "\x00" + text))
	return hex.EncodeToString(h[:])
}

func (c *cachingEmbedder) path(key string) string {
	return filepath.Join(c.dir, key[:2], key+".vec")
}

func (c *cachingEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	var missIdx []int
	var missText []string

	for i, t := range texts {
		if v := c.load(c.path(c.key(t))); v != nil {
			out[i] = v
			continue
		}
		missIdx = append(missIdx, i)
		missText = append(missText, t)
	}

	if len(missText) == 0 {
		return out, nil
	}

	vecs, err := c.base.Embed(ctx, missText)
	if err != nil {
		return nil, err
	}
	for j, v := range vecs {
		i := missIdx[j]
		out[i] = v
		c.store(c.path(c.key(missText[j])), v)
	}
	return out, nil
}

func (c *cachingEmbedder) load(path string) []float32 {
	b, err := os.ReadFile(path)
	if err != nil || len(b) == 0 || len(b)%4 != 0 {
		return nil
	}
	n := len(b) / 4
	v := make([]float32, n)
	for i := 0; i < n; i++ {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return v
}

func (c *cachingEmbedder) store(path string, v []float32) {
	if len(v) == 0 {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	b := make([]byte, len(v)*4)
	for i, f := range v {
		binary.LittleEndian.PutUint32(b[i*4:], math.Float32bits(f))
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, path)
}
