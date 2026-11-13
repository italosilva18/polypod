package wiring

import (
	"context"
	"log/slog"

	"github.com/costa/polypod/internal/automemory"
	"github.com/costa/polypod/internal/background"
	"github.com/costa/polypod/internal/checkpoint"
	"github.com/costa/polypod/internal/circuitbreaker"
	"github.com/costa/polypod/internal/clipboard"
	"github.com/costa/polypod/internal/compact"
	"github.com/costa/polypod/internal/convtree"
	"github.com/costa/polypod/internal/fallback"
	"github.com/costa/polypod/internal/loop"
	"github.com/costa/polypod/internal/mentions"
	"github.com/costa/polypod/internal/parallel"
	"github.com/costa/polypod/internal/provider"
	"github.com/costa/polypod/internal/smartroute"
	"github.com/costa/polypod/internal/theme"
	"github.com/costa/polypod/internal/undoredo"
	"github.com/costa/polypod/internal/watcher"
	"github.com/costa/polypod/internal/webcache"
	"github.com/costa/polypod/internal/worktree"
)

// SubsystemConfig provides all the inputs needed to wire the extra subsystems
// that were previously created and immediately discarded with `_ =`.
type SubsystemConfig struct {
	Logger      *slog.Logger
	DataDir     string
	WorkingDir  string
	Providers   []provider.Provider
	ProviderReg *provider.Registry

	// SkillExecFn is the function used by the parallel executor to run tools.
	SkillExecFn func(name, argsJSON string) (string, error)

	// CompactSvc is the compact service, wired externally because it depends on the AI client.
	CompactSvc *compact.CompactService

	// AutoMem is the auto-memory store, created before wiring (depends on data dir).
	AutoMem *automemory.Store
	// ActionTrigger runs a full agent task for an AI! comment in a changed file.
	ActionTrigger func(context.Context, watcher.Trigger)
	// OnFileEvent receives Added/Modified/Removed workspace file events in real time.
	OnFileEvent func(watcher.FileChangeEvent)
}

// ActiveSubsystems holds all the previously-discarded subsystems, now properly
// connected and accessible. Created by WireSubsystems.
type ActiveSubsystems struct {
	FallbackChain *fallback.Chain
	ParallelExec  *parallel.Executor
	CBRegistry    *circuitbreaker.Registry
	BgMgr         *background.Manager
	ConvTree      *convtree.Tree
	AutoMem       *automemory.Store
	CkptMgr       *checkpoint.Manager
	UndoMgr       *undoredo.Manager
	LoopMgr       *loop.Manager
	WebCache      *webcache.Cache
	WtMgr         *worktree.Manager
	ThemeState    *theme.Current
	ClipAvailable bool
	SmartTiers    map[string]smartroute.ModelTier
	WatcherCfg    watcher.WatchConfig
	FileIndex     *mentions.FileIndex
	CompactSvc    *compact.CompactService
	ActionTrigger func(context.Context, watcher.Trigger)
	OnFileEvent   func(watcher.FileChangeEvent)

	// fileWatcher is the active watcher instance (started on Start).
	fileWatcher   *watcher.Watcher
	watcherCancel context.CancelFunc
	providerCount int

	logger *slog.Logger
}

// WireSubsystems takes all the subsystem components that were previously created
// and discarded in main.go, and properly connects them to each other. Returns an
// ActiveSubsystems struct with Start/Shutdown/Stats methods.
func WireSubsystems(cfg SubsystemConfig) *ActiveSubsystems {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	// ===== 1. Fallback Chain — provider resilience =====
	fallbackChain := fallback.NewChain(cfg.Providers, logger)
	logger.Info("subsystems: fallback chain wired", "providers", len(cfg.Providers))

	// ===== 2. Parallel Executor — concurrent tool execution =====
	parallelExec := parallel.NewExecutor(cfg.SkillExecFn, logger)
	logger.Info("subsystems: parallel executor wired")

	// ===== 3. Circuit Breaker Registry — provider call protection =====
	cbRegistry := circuitbreaker.NewRegistry()
	if cfg.ProviderReg != nil {
		for _, name := range cfg.ProviderReg.List() {
			cbRegistry.Get(name) // pre-create breakers
		}
	}
	logger.Info("subsystems: circuit breaker registry wired")

	// ===== 4. Background Task Manager =====
	bgMgr := background.NewManager(logger)
	logger.Info("subsystems: background manager wired")

	// ===== 5. Conversation Tree — branching conversations =====
	convTree := convtree.NewTree()
	logger.Info("subsystems: conversation tree wired")

	// ===== 6. Auto-Memory =====
	autoMem := cfg.AutoMem
	if autoMem == nil {
		autoMem = automemory.NewStore(cfg.DataDir, logger)
	}

	// ===== 7. Checkpoint Manager =====
	ckptMgr := checkpoint.NewManager(cfg.DataDir, logger)
	logger.Info("subsystems: checkpoint manager wired")

	// ===== 8. Undo/Redo Manager — connected to checkpoint system =====
	undoMgr := undoredo.NewManager(logger)
	logger.Info("subsystems: undo/redo manager wired")

	// ===== 9. Loop Manager — recurring tasks =====
	loopMgr := loop.NewManager(logger)
	logger.Info("subsystems: loop manager wired")

	// ===== 10. Web Cache =====
	webCache := webcache.New(cfg.DataDir, 0)
	logger.Info("subsystems: web cache wired")

	// ===== 11. Worktree Manager =====
	wtMgr := worktree.NewManager("", logger)
	logger.Info("subsystems: worktree manager wired")

	// ===== 12. Theme State =====
	themeState := theme.NewCurrent()
	logger.Info("subsystems: theme wired", "name", themeState.Theme.Name)

	// ===== 13. Clipboard =====
	clipAvailable := clipboard.IsAvailable()
	logger.Info("subsystems: clipboard wired", "available", clipAvailable)

	// ===== 14. Smart Route Tiers =====
	smartTiers := smartroute.DefaultTiers
	logger.Info("subsystems: smart route tiers wired", "tiers", len(smartTiers))

	// ===== 15. File Watcher Config =====
	watcherCfg := watcher.DefaultConfig()
	logger.Info("subsystems: file watcher config wired", "interval", watcherCfg.Interval)

	// ===== 16. File Index — @file autocomplete =====
	workDir := cfg.WorkingDir
	if workDir == "" {
		workDir = "."
	}
	fileIndex := mentions.NewFileIndex(workDir)
	logger.Info("subsystems: file index wired")

	return &ActiveSubsystems{
		FallbackChain: fallbackChain,
		ParallelExec:  parallelExec,
		CBRegistry:    cbRegistry,
		BgMgr:         bgMgr,
		ConvTree:      convTree,
		AutoMem:       autoMem,
		CkptMgr:       ckptMgr,
		UndoMgr:       undoMgr,
		LoopMgr:       loopMgr,
		WebCache:      webCache,
		WtMgr:         wtMgr,
		ThemeState:    themeState,
		ClipAvailable: clipAvailable,
		SmartTiers:    smartTiers,
		WatcherCfg:    watcherCfg,
		FileIndex:     fileIndex,
		CompactSvc:    cfg.CompactSvc,
		ActionTrigger: cfg.ActionTrigger,
		OnFileEvent:   cfg.OnFileEvent,
		providerCount: len(cfg.Providers),
		logger:        logger,
	}
}

// Start starts all background subsystems that require active goroutines.
func (a *ActiveSubsystems) Start(ctx context.Context) {
	// Start file watcher in background
	w := watcher.New(a.WatcherCfg, a.logger)
	if a.OnFileEvent != nil {
		w.OnEvent(a.OnFileEvent)
	}
	w.OnChange(func(path string) {
		a.logger.Debug("file changed", "path", path)
		// Rebuild file index on changes for up-to-date autocomplete
		a.FileIndex.Rebuild()
	})
	w.OnTrigger(func(t watcher.Trigger) {
		a.logger.Info("AI trigger detected",
			"file", t.File,
			"line", t.Line,
			"type", t.Type,
			"prompt", t.Prompt,
		)
		if t.Type == "!" && a.ActionTrigger != nil {
			a.ActionTrigger(ctx, t)
		}
	})

	watchCtx, watchCancel := context.WithCancel(ctx)
	a.fileWatcher = w
	a.watcherCancel = watchCancel
	go func() {
		if err := w.Watch(watchCtx); err != nil {
			a.logger.Warn("file watcher stopped", "error", err)
		}
	}()

	a.logger.Info("subsystems started",
		"watcher", true,
		"bg_tasks", a.BgMgr.RunningCount(),
		"loops", a.LoopMgr.Count(),
	)
}

// Shutdown gracefully shuts down all subsystems.
func (a *ActiveSubsystems) Shutdown() {
	// Stop file watcher
	if a.watcherCancel != nil {
		a.watcherCancel()
	}

	// Stop all loops
	a.LoopMgr.StopAll()

	// Cleanup background tasks
	a.BgMgr.Cleanup()

	// Save auto-memory
	if a.AutoMem != nil {
		a.AutoMem.Save()
		a.logger.Info("subsystems: auto-memory saved")
	}

	// Clear web cache expired entries (save is automatic via Set)
	a.logger.Info("subsystems: web cache stats", "stats", a.WebCache.Stats())

	a.logger.Info("subsystems shutdown complete")
}

// Stats returns aggregate statistics from all subsystems.
func (a *ActiveSubsystems) Stats() map[string]any {
	stats := map[string]any{
		"fallback_chain_providers": a.providerCount,
		"circuit_breakers":         a.CBRegistry.Stats(),
		"bg_tasks_running":         a.BgMgr.RunningCount(),
		"conv_tree_branches":       a.ConvTree.Branches(),
		"conv_tree_messages":       len(a.ConvTree.Nodes) - 1, // exclude root
		"auto_memory_facts":        a.AutoMem.Count(),
		"checkpoint_count":         len(a.CkptMgr.List()),
		"undo_available":           a.UndoMgr.CanUndo(),
		"active_loops":             a.LoopMgr.Count(),
		"web_cache":                a.WebCache.Stats(),
		"clipboard_available":      a.ClipAvailable,
		"theme":                    a.ThemeState.Theme.Name,
		"smart_tiers":              len(a.SmartTiers),
		"watcher_interval":         a.WatcherCfg.Interval.String(),
	}
	return stats
}

// ProviderCallWithBreaker wraps a provider call with circuit breaker protection
// and fallback chain. If the circuit breaker for the provider is open, it falls
// back to the next available provider via the fallback chain.
func (a *ActiveSubsystems) ProviderCallWithBreaker(
	ctx context.Context,
	providerName string,
	req provider.ChatRequest,
) (*provider.ChatResponse, error) {
	// Check circuit breaker for the requested provider
	breaker := a.CBRegistry.Get(providerName)
	if !breaker.Allow() {
		a.logger.Warn("circuit breaker open, using fallback chain", "provider", providerName)
		// Use fallback chain when primary provider is tripped
		resp, err := a.FallbackChain.Chat(ctx, req)
		if err != nil {
			return nil, err
		}
		return resp, nil
	}

	// Try fallback chain (which tries primary first, then falls back)
	resp, err := a.FallbackChain.Chat(ctx, req)
	if err != nil {
		breaker.RecordFailure()
		return nil, err
	}

	breaker.RecordSuccess()
	return resp, nil
}

// ProviderStreamWithBreaker is the streaming variant of ProviderCallWithBreaker.
func (a *ActiveSubsystems) ProviderStreamWithBreaker(
	ctx context.Context,
	providerName string,
	req provider.ChatRequest,
	callback provider.StreamCallback,
) (*provider.ChatResponse, error) {
	breaker := a.CBRegistry.Get(providerName)
	if !breaker.Allow() {
		a.logger.Warn("circuit breaker open (stream), using fallback chain", "provider", providerName)
		return a.FallbackChain.ChatStream(ctx, req, callback)
	}

	resp, err := a.FallbackChain.ChatStream(ctx, req, callback)
	if err != nil {
		breaker.RecordFailure()
		return nil, err
	}

	breaker.RecordSuccess()
	return resp, nil
}

// ExecuteToolsParallel runs a batch of tool calls through the parallel executor.
func (a *ActiveSubsystems) ExecuteToolsParallel(calls []parallel.ToolCall) []parallel.ToolResult {
	return a.ParallelExec.Execute(calls)
}

// TrackConversation adds a message to the conversation tree.
func (a *ActiveSubsystems) TrackConversation(role, content string) {
	a.ConvTree.AddMessage(role, content)
}

// CreateCheckpoint snapshots files before a destructive operation.
func (a *ActiveSubsystems) CreateCheckpoint(name string, files []string, message string) error {
	_, err := a.CkptMgr.Create(name, files, message)
	return err
}

// BeforeFileChange records undo state before modifying files.
func (a *ActiveSubsystems) BeforeFileChange(description string, paths []string) int {
	return a.UndoMgr.BeforeChange(description, paths)
}

// RouteToOptimalModel uses smart routing to select the best model for a prompt.
func (a *ActiveSubsystems) RouteToOptimalModel(providerName, prompt string) (string, smartroute.Complexity) {
	return smartroute.Route(providerName, prompt)
}

// CacheWebResult stores a web fetch result in the cache.
func (a *ActiveSubsystems) CacheWebResult(key string, result webcache.CachedResult) {
	a.WebCache.Set(key, result)
}

// GetCachedWebResult retrieves a cached web result, or nil if not found/expired.
func (a *ActiveSubsystems) GetCachedWebResult(key string) *webcache.CachedResult {
	return a.WebCache.Get(key)
}

// CopyToClipboard copies text to clipboard if available.
func (a *ActiveSubsystems) CopyToClipboard(text string) error {
	if !a.ClipAvailable {
		return nil
	}
	return clipboard.Copy(text)
}
