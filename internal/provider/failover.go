package provider

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	official "github.com/openai/openai-go/v3"
	openai "github.com/sashabaranov/go-openai"
)

// ErrProviderUnavailable marks authentication or service availability failures.
var ErrProviderUnavailable = errors.New("provedor indisponivel")

// Failover retries an individual completion on an explicitly configured backend.
// Tool execution remains outside this layer, so completed actions are never replayed.
type Failover struct {
	primary, secondary Provider
	model              string
	logger             *slog.Logger
}

func NewFailover(primary, secondary Provider, fallbackModel string, logger *slog.Logger) *Failover {
	if logger == nil {
		logger = slog.Default()
	}
	return &Failover{primary: primary, secondary: secondary, model: fallbackModel, logger: logger}
}
func (f *Failover) Name() string                   { return f.primary.Name() }
func (f *Failover) SupportsVision() bool           { return f.primary.SupportsVision() }
func (f *Failover) SupportsTools() bool            { return f.primary.SupportsTools() }
func (f *Failover) SupportsStructuredOutput() bool { return f.primary.SupportsStructuredOutput() }

func (f *Failover) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	resp, err := f.primary.Chat(ctx, req)
	if !f.shouldSwitch(ctx, req, err) {
		return resp, err
	}
	req.Model = f.model
	resp, err = f.secondary.Chat(ctx, req)
	if resp != nil {
		resp.Model = f.model
	}
	return resp, err
}
func (f *Failover) ChatStream(ctx context.Context, req ChatRequest, callback StreamCallback) (*ChatResponse, error) {
	emitted := false
	resp, err := f.primary.ChatStream(ctx, req, func(delta StreamDelta) {
		if delta.Content != "" || len(delta.ToolCalls) > 0 || delta.Done {
			emitted = true
		}
		if callback != nil {
			callback(delta)
		}
	})
	// Never concatenate two providers' partial outputs or tool arguments.
	if emitted || !f.shouldSwitch(ctx, req, err) {
		return resp, err
	}
	req.Model = f.model
	resp, err = f.secondary.ChatStream(ctx, req, callback)
	if resp != nil {
		resp.Model = f.model
	}
	return resp, err
}

type completedCallStreamProvider interface {
	ChatStreamWithCompletedCalls(
		ctx context.Context,
		req ChatRequest,
		callback StreamCallback,
		itemReady func(index int64, call *ToolCall),
	) (*ChatResponse, error)
}

// ChatStreamWithCompletedCalls preserves the ordinary failover threshold while
// allowing finalized tool calls to reach the runtime early. Until the primary
// emits a visible stream delta, its completed calls are buffered: a failure
// can still switch providers without executing tools from the failed attempt.
func (f *Failover) ChatStreamWithCompletedCalls(
	ctx context.Context,
	req ChatRequest,
	callback StreamCallback,
	itemReady func(index int64, call *ToolCall),
) (*ChatResponse, error) {
	type completedItem struct {
		index int64
		call  *ToolCall
	}
	var pending []completedItem
	emitted := false
	flushPending := func() {
		if itemReady != nil {
			for _, item := range pending {
				itemReady(item.index, item.call)
			}
		}
		pending = nil
	}
	primaryItemReady := func(index int64, call *ToolCall) {
		if emitted {
			if itemReady != nil {
				itemReady(index, call)
			}
			return
		}
		if itemReady != nil {
			var copyCall *ToolCall
			if call != nil {
				copy := *call
				copyCall = &copy
			}
			pending = append(pending, completedItem{index: index, call: copyCall})
		}
	}
	primaryCallback := func(delta StreamDelta) {
		if delta.Content != "" || len(delta.ToolCalls) > 0 || delta.Done {
			emitted = true
		}
		if callback != nil {
			callback(delta)
		}
		if emitted {
			flushPending()
		}
	}

	var resp *ChatResponse
	var err error
	if streamer, ok := f.primary.(completedCallStreamProvider); ok {
		resp, err = streamer.ChatStreamWithCompletedCalls(ctx, req, primaryCallback, primaryItemReady)
	} else {
		resp, err = f.primary.ChatStream(ctx, req, primaryCallback)
	}
	if emitted || !f.shouldSwitch(ctx, req, err) {
		if err == nil {
			flushPending()
		}
		return resp, err
	}
	// The primary never emitted externally visible output. Discard all of its
	// completed-item notifications before invoking the secondary provider.
	pending = nil
	req.Model = f.model
	if streamer, ok := f.secondary.(completedCallStreamProvider); ok {
		resp, err = streamer.ChatStreamWithCompletedCalls(ctx, req, callback, itemReady)
	} else {
		resp, err = f.secondary.ChatStream(ctx, req, callback)
	}
	if resp != nil {
		resp.Model = f.model
	}
	return resp, err
}

func (f *Failover) shouldSwitch(ctx context.Context, req ChatRequest, err error) bool {
	if ctx.Err() != nil || f.model == "" || !IsFailoverError(err) {
		return false
	}
	if len(req.Tools) > 0 && !f.secondary.SupportsTools() {
		return false
	}
	if req.ResponseFormat != nil && !f.secondary.SupportsStructuredOutput() {
		return false
	}
	for _, msg := range req.Messages {
		if (len(msg.ToolCalls) > 0 || msg.ToolCallID != "") && !f.secondary.SupportsTools() {
			return false
		}
		for _, part := range msg.Parts {
			if (part.Type == "image_url" || part.Type == "image_base64") && !f.secondary.SupportsVision() {
				return false
			}
		}
	}
	// Do not log upstream error bodies: they can contain credentials or prompt data.
	f.logger.Warn("ai provider failover", "from", f.primary.Name(), "to", f.secondary.Name(), "model", f.model, "reason", "provider_unavailable")
	return true
}

// IsFailoverError classifies transport failures and typed HTTP provider failures.
// Invalid requests, safety rejections and caller cancellation are not failover triggers.
func IsFailoverError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if errors.Is(err, ErrProviderUnavailable) {
		return true
	}
	var officialErr *official.Error
	if errors.As(err, &officialErr) {
		return failoverStatus(officialErr.StatusCode)
	}
	var anthropicErr *anthropic.Error
	if errors.As(err, &anthropicErr) {
		return failoverStatus(anthropicErr.StatusCode)
	}
	var apiErr *openai.APIError
	if errors.As(err, &apiErr) {
		return failoverStatus(apiErr.HTTPStatusCode)
	}
	var requestErr *openai.RequestError
	if errors.As(err, &requestErr) {
		return failoverStatus(requestErr.HTTPStatusCode)
	}
	var netErr net.Error
	return errors.As(err, &netErr) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF)
}
func failoverStatus(code int) bool {
	return code == 401 || code == 402 || code == 408 || code == 429 || code >= 500 && code <= 599
}
