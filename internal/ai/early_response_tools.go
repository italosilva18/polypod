package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
	"time"

	providerapi "github.com/costa/polypod/internal/provider"
)

// completedCallStreamer is implemented only by providers that expose an
// explicit boundary for each finished function call. In particular, the
// Chat Completions stream does not provide such a boundary.
type completedCallStreamer interface {
	ChatStreamWithCompletedCalls(
		ctx context.Context,
		req providerapi.ChatRequest,
		callback providerapi.StreamCallback,
		itemReady func(index int64, call *providerapi.ToolCall),
	) (*providerapi.ChatResponse, error)
}

type earlyResponseCall struct {
	call    providerapi.ToolCall
	result  ToolCallResult
	elapsed time.Duration
}

// earlyResponseTools schedules only local, concurrency-safe reads. Calls are
// considered in output-item order, so an incomplete or write-capable call
// prevents all later reads from overtaking it.
type earlyResponseTools struct {
	client  *Client
	ctx     context.Context
	cancel  context.CancelFunc
	ready   map[int64]*providerapi.ToolCall
	jobs    map[string]*earlyResponseCall
	sem     chan struct{}
	queued  int64
	blocked bool
	wg      sync.WaitGroup
}

func newEarlyResponseTools(ctx context.Context, client *Client) *earlyResponseTools {
	toolCtx, cancel := context.WithCancel(ctx)
	return &earlyResponseTools{
		client: client,
		ctx:    toolCtx,
		cancel: cancel,
		ready:  make(map[int64]*providerapi.ToolCall),
		jobs:   make(map[string]*earlyResponseCall),
		sem:    make(chan struct{}, maxConcurrentReadOnly),
	}
}

func (e *earlyResponseTools) itemReady(index int64, call *providerapi.ToolCall) {
	// The provider can report a call at both arguments.done and item.done.
	if index < e.queued {
		return
	}
	if _, exists := e.ready[index]; exists {
		return
	}
	e.ready[index] = call
	for {
		call, ok := e.ready[e.queued]
		if !ok {
			return
		}
		delete(e.ready, e.queued)
		e.queued++
		if call == nil || e.blocked {
			continue
		}
		if !e.canRunEarly(*call) {
			e.blocked = true
			continue
		}
		if _, duplicate := e.jobs[call.ID]; duplicate {
			// A call ID represents one execution even if a provider repeats it.
			e.blocked = true
			continue
		}
		job := &earlyResponseCall{call: *call}
		e.jobs[call.ID] = job
		e.wg.Add(1)
		go e.execute(job)
	}
}

func (e *earlyResponseTools) canRunEarly(call providerapi.ToolCall) bool {
	if call.ID == "" || call.Name == "" || !localReadTool(call.Name) {
		return false
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil || args == nil {
		return false
	}
	concurrent, serial := e.client.streamingToolMgr.PartitionCalls([]ToolCallInfo{{
		ID: call.ID, Name: call.Name, Arguments: call.Arguments,
	}})
	return len(concurrent) == 1 && len(serial) == 0
}

// Keep early execution to local file reads and searches. Some tools marked
// read-only still send messages or consume external services, so the manager's
// read-only flag alone is insufficient for speculative execution.
func localReadTool(name string) bool {
	switch name {
	case "read_file_dir", "glob", "grep", "glob_search", "grep_search":
		return true
	default:
		return false
	}
}

func (e *earlyResponseTools) execute(job *earlyResponseCall) {
	defer e.wg.Done()
	select {
	case e.sem <- struct{}{}:
		defer func() { <-e.sem }()
	case <-e.ctx.Done():
		job.result = ToolCallResult{ID: job.call.ID, Name: job.call.Name, Content: "cancelled: context done", IsError: true}
		return
	}
	if e.ctx.Err() != nil {
		job.result = ToolCallResult{ID: job.call.ID, Name: job.call.Name, Content: "cancelled: context done", IsError: true}
		return
	}
	e.client.fireResponseToolStart(e.ctx, job.call)
	started := time.Now()
	job.result = e.client.streamingToolMgr.executeSingle(e.ctx, ToolCallInfo{
		ID: job.call.ID, Name: job.call.Name, Arguments: job.call.Arguments,
	})
	job.elapsed = time.Since(started)
	e.client.fireResponseToolDone(e.ctx, job.result, job.elapsed)
}

func (e *earlyResponseTools) finish(abort bool) {
	if abort {
		e.cancel()
	}
	e.wg.Wait()
	e.cancel()
}

func (e *earlyResponseTools) validateFinal(calls []providerapi.ToolCall) error {
	final := make(map[string]providerapi.ToolCall, len(calls))
	for _, call := range calls {
		if call.ID != "" {
			if prior, duplicate := final[call.ID]; duplicate && !equivalentToolCall(prior, call) {
				return fmt.Errorf("resposta do provedor repetiu call ID com argumentos diferentes")
			}
			final[call.ID] = call
		}
	}
	for id, job := range e.jobs {
		call, ok := final[id]
		if !ok || !equivalentToolCall(call, job.call) {
			return fmt.Errorf("resposta final do provedor divergiu da chamada concluida")
		}
	}
	return nil
}

// Providers may serialize an unchanged argument object with different JSON
// whitespace or key order in the final response. Compare the parsed values
// rather than the original bytes, while rejecting any changed tool input.
func equivalentToolCall(a, b providerapi.ToolCall) bool {
	if a.ID != b.ID || a.Name != b.Name {
		return false
	}
	var aArgs, bArgs map[string]any
	if json.Unmarshal([]byte(a.Arguments), &aArgs) != nil || json.Unmarshal([]byte(b.Arguments), &bArgs) != nil {
		return false
	}
	return reflect.DeepEqual(aArgs, bArgs)
}
