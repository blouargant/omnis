// Package teammates implements the article's "persistent teammates with
// JSONL mailboxes" (Phase 3 / s09), the FSM communication protocol
// (Phase 3 / s10) and the Redis pub/sub upgrade (Phase 6 / s22). The
// public Backend interface lets agents send/receive messages without
// knowing whether the transport is files or Redis.
package teammates

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// Message is one envelope on a mailbox.
type Message struct {
	From string    `json:"from"`
	Body string    `json:"body"`
	When time.Time `json:"when"`
}

// Backend abstracts the mailbox transport.
type Backend interface {
	Send(ctx context.Context, to string, m Message) error
	Receive(ctx context.Context, name string, timeout time.Duration) (*Message, error)
	Close() error
}

// JSONLBackend is a file-backed mailbox: one .jsonl file per agent.
// Reads consume the file (truncate after read).
type JSONLBackend struct {
	dir string
	mu  sync.Mutex
}

// NewJSONLBackend creates the directory if needed.
func NewJSONLBackend(dir string) (*JSONLBackend, error) {
	if dir == "" {
		dir = ".mailboxes"
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &JSONLBackend{dir: dir}, nil
}

func (b *JSONLBackend) inbox(name string) string {
	return filepath.Join(b.dir, name+".jsonl")
}

// Send appends one message to the recipient's inbox file.
func (b *JSONLBackend) Send(_ context.Context, to string, m Message) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if m.When.IsZero() {
		m.When = time.Now()
	}
	f, err := os.OpenFile(b.inbox(to), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	_, err = f.Write(append(data, '\n'))
	return err
}

// Receive polls the mailbox until a message arrives or timeout elapses.
// Returns nil, nil on timeout.
func (b *JSONLBackend) Receive(ctx context.Context, name string, timeout time.Duration) (*Message, error) {
	deadline := time.Now().Add(timeout)
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		b.mu.Lock()
		path := b.inbox(name)
		data, err := os.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			b.mu.Unlock()
			return nil, err
		}
		if len(data) > 0 {
			lines := splitLines(data)
			if len(lines) > 0 {
				var m Message
				if err := json.Unmarshal(lines[0], &m); err != nil {
					b.mu.Unlock()
					return nil, err
				}
				rest := lines[1:]
				if len(rest) == 0 {
					_ = os.Truncate(path, 0)
				} else {
					var out []byte
					for _, l := range rest {
						out = append(out, l...)
						out = append(out, '\n')
					}
					_ = os.WriteFile(path, out, 0o644)
				}
				b.mu.Unlock()
				return &m, nil
			}
		}
		b.mu.Unlock()
		if time.Now().After(deadline) {
			return nil, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-tick.C:
		}
	}
}

// Close is a no-op for the file backend.
func (b *JSONLBackend) Close() error { return nil }

func splitLines(data []byte) [][]byte {
	var out [][]byte
	start := 0
	for i, c := range data {
		if c == '\n' {
			if i > start {
				out = append(out, data[start:i])
			}
			start = i + 1
		}
	}
	if start < len(data) {
		out = append(out, data[start:])
	}
	return out
}

// RedisBackend uses Redis pub/sub channels for instant cross-process
// delivery.
type RedisBackend struct {
	c       *redis.Client
	subs    sync.Map // name -> *redis.PubSub
	subsMu  sync.Mutex
	closeFn func() error
}

// NewRedisBackend dials the URL (e.g. redis://localhost:6379).
func NewRedisBackend(url string) (*RedisBackend, error) {
	opts, err := redis.ParseURL(url)
	if err != nil {
		return nil, err
	}
	c := redis.NewClient(opts)
	if err := c.Ping(context.Background()).Err(); err != nil {
		return nil, fmt.Errorf("redis ping: %w", err)
	}
	return &RedisBackend{c: c, closeFn: c.Close}, nil
}

func channel(name string) string { return "agent:" + name + ":inbox" }

// Send publishes the message JSON to the recipient's channel.
func (b *RedisBackend) Send(ctx context.Context, to string, m Message) error {
	if m.When.IsZero() {
		m.When = time.Now()
	}
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return b.c.Publish(ctx, channel(to), data).Err()
}

// Receive blocks on the named channel for up to timeout.
func (b *RedisBackend) Receive(ctx context.Context, name string, timeout time.Duration) (*Message, error) {
	b.subsMu.Lock()
	subAny, ok := b.subs.Load(name)
	var sub *redis.PubSub
	if ok {
		sub = subAny.(*redis.PubSub)
	} else {
		sub = b.c.Subscribe(ctx, channel(name))
		b.subs.Store(name, sub)
	}
	b.subsMu.Unlock()
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	msg, err := sub.ReceiveMessage(cctx)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, nil
		}
		return nil, err
	}
	var m Message
	if err := json.Unmarshal([]byte(msg.Payload), &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// Close tears down all subscriptions and the underlying client.
func (b *RedisBackend) Close() error {
	b.subs.Range(func(_, v any) bool {
		_ = v.(*redis.PubSub).Close()
		return true
	})
	if b.closeFn != nil {
		return b.closeFn()
	}
	return nil
}

// ChooseBackend returns Redis if REDIS_URL is set, otherwise a JSONL
// mailbox under .mailboxes/.
func ChooseBackend() (Backend, error) {
	if u := os.Getenv("REDIS_URL"); u != "" {
		return NewRedisBackend(u)
	}
	return NewJSONLBackend(".mailboxes")
}
