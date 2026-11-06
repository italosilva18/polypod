// Package analytics provides an event pipeline that collects analytics events
// and flushes them to one or more sinks (file, HTTP, etc.).
//
// Ported from the TypeScript analytics service in src/services/analytics/.
package analytics

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/costa/polypod/internal/skill"
)

// ---------------------------------------------------------------------------
// Event
// ---------------------------------------------------------------------------

// Event represents a single analytics event.
type Event struct {
	Name       string         `json:"name"`
	Properties map[string]any `json:"properties,omitempty"`
	Timestamp  time.Time      `json:"timestamp"`
	SessionID  string         `json:"session_id,omitempty"`
	UserID     string         `json:"user_id,omitempty"`
}

// ---------------------------------------------------------------------------
// Sink interface
// ---------------------------------------------------------------------------

// Sink receives batches of events and writes them to a backend.
type Sink interface {
	// Send writes a batch of events to the backend.
	Send(ctx context.Context, events []Event) error
	// Flush ensures all buffered data has been written.
	Flush(ctx context.Context) error
	// Close releases any resources held by the sink.
	Close() error
}

// ---------------------------------------------------------------------------
// FileSink
// ---------------------------------------------------------------------------

// FileSink appends events as JSON lines to a file.
type FileSink struct {
	mu   sync.Mutex
	file *os.File
	enc  *json.Encoder
}

// NewFileSink opens (or creates) the file at path in append-only mode.
func NewFileSink(path string) (*FileSink, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("analytics: open file sink %q: %w", path, err)
	}
	return &FileSink{
		file: f,
		enc:  json.NewEncoder(f),
	}, nil
}

func (s *FileSink) Send(_ context.Context, events []Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range events {
		if err := s.enc.Encode(&events[i]); err != nil {
			return fmt.Errorf("analytics: file sink encode: %w", err)
		}
	}
	return nil
}

func (s *FileSink) Flush(_ context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.file.Sync()
}

func (s *FileSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.file.Close()
}

// ---------------------------------------------------------------------------
// HTTPSink
// ---------------------------------------------------------------------------

// HTTPSinkConfig configures an HTTPSink.
type HTTPSinkConfig struct {
	URL           string        // Endpoint that receives POST requests.
	BatchSize     int           // Max events per HTTP request.
	FlushInterval time.Duration // Max time between flushes.
	AuthHeader    string        // Value for the Authorization header (optional).
	Client        *http.Client  // HTTP client to use (defaults to http.DefaultClient).
}

// HTTPSink batches events and POSTs them as JSON to a configurable URL.
type HTTPSink struct {
	cfg    HTTPSinkConfig
	client *http.Client

	mu  sync.Mutex
	buf []Event
	// timer triggers a periodic flush; nil until the first event arrives.
	timer *time.Timer
	done  chan struct{}
}

// NewHTTPSink creates an HTTPSink with the given configuration.
func NewHTTPSink(cfg HTTPSinkConfig) *HTTPSink {
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 100
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = 30 * time.Second
	}
	client := cfg.Client
	if client == nil {
		client = http.DefaultClient
	}
	return &HTTPSink{
		cfg:    cfg,
		client: client,
		buf:    make([]Event, 0, cfg.BatchSize),
		done:   make(chan struct{}),
	}
}

func (s *HTTPSink) Send(ctx context.Context, events []Event) error {
	s.mu.Lock()
	s.buf = append(s.buf, events...)
	shouldFlush := len(s.buf) >= s.cfg.BatchSize
	// Start a flush timer on first buffered event.
	if s.timer == nil && len(s.buf) > 0 {
		s.timer = time.AfterFunc(s.cfg.FlushInterval, func() {
			_ = s.Flush(context.Background())
		})
	}
	s.mu.Unlock()

	if shouldFlush {
		return s.Flush(ctx)
	}
	return nil
}

func (s *HTTPSink) Flush(ctx context.Context) error {
	s.mu.Lock()
	if len(s.buf) == 0 {
		s.mu.Unlock()
		return nil
	}
	batch := s.buf
	s.buf = make([]Event, 0, s.cfg.BatchSize)
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	s.mu.Unlock()

	return s.post(ctx, batch)
}

func (s *HTTPSink) Close() error {
	if s.timer != nil {
		s.timer.Stop()
	}
	close(s.done)
	return nil
}

func (s *HTTPSink) post(ctx context.Context, events []Event) error {
	body, err := json.Marshal(events)
	if err != nil {
		return fmt.Errorf("analytics: http sink marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.URL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("analytics: http sink request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if s.cfg.AuthHeader != "" {
		req.Header.Set("Authorization", s.cfg.AuthHeader)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("analytics: http sink post: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("analytics: http sink: server returned %d", resp.StatusCode)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Pipeline
// ---------------------------------------------------------------------------

// Pipeline collects events via Track and flushes them to the configured sinks
// either when the batch is full or at a regular interval.
type Pipeline struct {
	sinks         []Sink
	batchSize     int
	flushInterval time.Duration
	ch            chan Event
	done          chan struct{}
	wg            sync.WaitGroup
}

// NewPipeline creates a Pipeline that dispatches to the given sinks.
func NewPipeline(sinks []Sink, batchSize int, flushInterval time.Duration) *Pipeline {
	if batchSize <= 0 {
		batchSize = 100
	}
	if flushInterval <= 0 {
		flushInterval = 30 * time.Second
	}
	return &Pipeline{
		sinks:         sinks,
		batchSize:     batchSize,
		flushInterval: flushInterval,
		ch:            make(chan Event, batchSize*2),
		done:          make(chan struct{}),
	}
}

// Track enqueues an event for asynchronous delivery. It never blocks; if the
// channel is full the event is dropped silently to avoid back-pressure on the
// caller (same design as the TS version that uses fire-and-forget).
func (p *Pipeline) Track(event Event) {
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}
	select {
	case p.ch <- event:
	default:
		// Channel full — drop the event to keep Track non-blocking.
	}
}

// Start begins a background goroutine that reads from the event channel,
// batches events, and flushes them to all sinks. It stops when ctx is
// cancelled or Shutdown is called.
func (p *Pipeline) Start(ctx context.Context) {
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		ticker := time.NewTicker(p.flushInterval)
		defer ticker.Stop()

		batch := make([]Event, 0, p.batchSize)

		flush := func() {
			if len(batch) == 0 {
				return
			}
			// Copy batch so sinks can process asynchronously if needed.
			sending := make([]Event, len(batch))
			copy(sending, batch)
			batch = batch[:0]
			for _, sink := range p.sinks {
				_ = sink.Send(ctx, sending)
			}
		}

		for {
			select {
			case ev, ok := <-p.ch:
				if !ok {
					flush()
					return
				}
				batch = append(batch, ev)
				if len(batch) >= p.batchSize {
					flush()
				}
			case <-ticker.C:
				flush()
			case <-ctx.Done():
				// Drain remaining events from the channel.
				for {
					select {
					case ev, ok := <-p.ch:
						if !ok {
							flush()
							return
						}
						batch = append(batch, ev)
					default:
						flush()
						return
					}
				}
			case <-p.done:
				// Drain remaining events from the channel.
				for {
					select {
					case ev, ok := <-p.ch:
						if !ok {
							flush()
							return
						}
						batch = append(batch, ev)
					default:
						flush()
						return
					}
				}
			}
		}
	}()
}

// Shutdown flushes remaining events and closes all sinks. It blocks until
// everything has been flushed or ctx expires.
func (p *Pipeline) Shutdown(ctx context.Context) error {
	close(p.done)
	close(p.ch)

	// Wait for the background goroutine to finish draining.
	waitCh := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(waitCh)
	}()
	select {
	case <-waitCh:
	case <-ctx.Done():
		return ctx.Err()
	}

	var firstErr error
	for _, sink := range p.sinks {
		if err := sink.Flush(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
		if err := sink.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// ---------------------------------------------------------------------------
// Skill registration
// ---------------------------------------------------------------------------

// RegisterSkills registers analytics-related skills into the skill registry.
// Currently a no-op placeholder; analytics is a background service and does
// not expose user-facing tools, but the hook is kept for consistency with the
// RegisterSkills convention used across the codebase.
func RegisterSkills(reg *skill.Registry) {
	// No user-facing skills to register at this time.
	_ = reg
}
