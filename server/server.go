package main

import (
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"google.golang.org/adk/runner"

	"github.com/blouargant/agent-toolkit/core/events"
)

type serverDeps struct {
	Token       string
	Runner      *runner.Runner
	Registry    *registry
	WebDir      string
	AgentEvents *agentEventBroadcaster
}

// agentBusEvent is a single event from the shared event bus forwarded to an
// active SSE stream.
type agentBusEvent struct {
	Event   string
	Payload map[string]any
}

// agentEventBroadcaster registers once on the event bus and fans out sub-agent
// tool events to any number of per-request channels. Using a single persistent
// handler avoids handler accumulation on the bus across many requests.
type agentEventBroadcaster struct {
	mu   sync.RWMutex
	subs map[chan<- agentBusEvent]struct{}
}

func newAgentEventBroadcaster(bus *events.Bus) *agentEventBroadcaster {
	b := &agentEventBroadcaster{subs: make(map[chan<- agentBusEvent]struct{})}
	forward := func(ev string, p map[string]any) {
		// Skip leader events — they are already surfaced by the ADK event stream.
		if agent, _ := p["agent"].(string); agent == "leader" {
			return
		}
		b.mu.RLock()
		for ch := range b.subs {
			select {
			case ch <- agentBusEvent{ev, p}:
			default: // slow subscriber; drop rather than block the tool callback
			}
		}
		b.mu.RUnlock()
	}
	bus.On(events.EventBeforeTool, forward)
	bus.On(events.EventAfterTool, forward)
	bus.On(events.EventToolError, forward)
	return b
}

func (b *agentEventBroadcaster) subscribe() chan agentBusEvent {
	ch := make(chan agentBusEvent, 64)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

func (b *agentEventBroadcaster) unsubscribe(ch chan agentBusEvent) {
	b.mu.Lock()
	delete(b.subs, ch)
	b.mu.Unlock()
	// Drain so any blocked broadcaster send can proceed.
	for len(ch) > 0 {
		<-ch
	}
}

func newEngine(d serverDeps) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery(), requestLogger())

	// Static UI (no auth — served at the root).
	indexPath := filepath.Join(d.WebDir, "index.html")
	r.StaticFile("/", indexPath)
	r.StaticFile("/index.html", indexPath)
	r.Static("/assets", d.WebDir)

	api := r.Group("/api")
	api.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	auth := api.Group("", authMiddleware(d.Token))
	auth.POST("/sessions", func(c *gin.Context) {
		meta := d.Registry.New()
		c.JSON(http.StatusCreated, gin.H{
			"session_id": meta.ID,
			"created_at": meta.CreatedAt,
		})
	})
	auth.GET("/sessions", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"sessions": d.Registry.List()})
	})
	auth.DELETE("/sessions/:id", func(c *gin.Context) {
		id := c.Param("id")
		if !d.Registry.Delete(id) {
			c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
			return
		}
		c.Status(http.StatusNoContent)
	})
	auth.POST("/sessions/:id/messages", handleMessages(d))

	return r
}

func requestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		path := c.Request.URL.Path
		if strings.HasPrefix(path, "/assets/") {
			return
		}
		gin.DefaultWriter.Write([]byte("" +
			time.Now().Format("15:04:05") + " " +
			c.Request.Method + " " + path + " " +
			itoa(c.Writer.Status()) + " " +
			time.Since(start).Truncate(time.Millisecond).String() + "\n"))
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
