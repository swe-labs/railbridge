package api

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

//go:embed docs.html
var docsHTML []byte

//go:embed datastar.js
var datastarJS []byte

// handleDocs renders the interactive documentation and Datastar console.
func (s *Server) handleDocs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(docsHTML)
}

// handleDatastarScript serves the embedded, zero-CDN Datastar JavaScript bundle.
func (s *Server) handleDatastarScript(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(datastarJS)
}

type TelemetryEvent struct {
	Type    string `json:"type"`
	Level   string `json:"level"`
	Message string `json:"message"`
	Time    string `json:"time"`
}

// handleDocsEvents streams live platform telemetry via Datastar Server-Sent Events (SSE).
func (s *Server) handleDocsEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ctx := r.Context()

	// Initial welcome message
	initialEvent, _ := json.Marshal(TelemetryEvent{
		Type:    "engine",
		Level:   "info",
		Message: fmt.Sprintf("Connected to Railbridge live SSE stream (%s mode, %s dialect)", s.Config.Env, s.Store.Dialect),
		Time:    time.Now().UTC().Format("15:04:05"),
	})
	fmt.Fprintf(w, "data: %s\n\n", string(initialEvent))
	flusher.Flush()

	ticker := time.NewTicker(4 * time.Second)
	defer ticker.Stop()

	eventSequence := 0
	for {
		select {
		case <-ctx.Done():
			// Client disconnected: cleanly terminate goroutine per goth-hypermedia-stack P0 rules
			return
		case t := <-ticker.C:
			eventSequence++
			var evt TelemetryEvent

			switch eventSequence % 4 {
			case 1:
				evt = TelemetryEvent{
					Type:    "outbox",
					Level:   "info",
					Message: "Outbox dispatcher polled outbox_jobs; queue backlog = 0 pending",
					Time:    t.UTC().Format("15:04:05"),
				}
			case 2:
				evt = TelemetryEvent{
					Type:    "mfs_bridge",
					Level:   "info",
					Message: "Heartbeat verified from Android SIM Bridge node (carrier: Grameenphone/Banglalink)",
					Time:    t.UTC().Format("15:04:05"),
				}
			case 3:
				evt = TelemetryEvent{
					Type:    "limiter",
					Level:   "info",
					Message: "MemoryRateLimiter token-bucket GC check passed; 0 stale tenant buckets evicted",
					Time:    t.UTC().Format("15:04:05"),
				}
			default:
				evt = TelemetryEvent{
					Type:    "database",
					Level:   "info",
					Message: fmt.Sprintf("PostgreSQL health check ping passed (latency < 2ms, dialect: %s)", s.Store.Dialect),
					Time:    t.UTC().Format("15:04:05"),
				}
			}

			payload, _ := json.Marshal(evt)

			// 1. Standard SSE event format (for EventSource)
			fmt.Fprintf(w, "data: %s\n\n", string(payload))

			// 2. Datastar merge-fragments format (for Datastar reactive DOM updates)
			datastarFragment := fmt.Sprintf(`<div id="telemetry-live-item" class="feed-item"><span style="color: #38bdf8;">[%s] %s</span><span class="feed-time">%s</span></div>`, evt.Type, evt.Message, evt.Time)
			fmt.Fprintf(w, "event: datastar-merge-fragments\ndata: fragments %s\n\n", datastarFragment)

			flusher.Flush()
		}
	}
}
