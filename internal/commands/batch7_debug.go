// Package commands — batch7_debug.go
// Migrated debug / diagnostics slash-command implementations.
//
// These six commands cover lightweight runtime introspection
// (/heapdump, /perf-issue, /debug-tool-call) and three placeholders
// for the rate-limit / cache subsystems that will be wired up later
// (/break-cache, /mock-limits, /reset-limits).
//
// All user-facing strings are Brazilian Portuguese, in keeping with
// the project convention. Code, comments and identifiers stay in
// English.
package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"sync"
	"time"

	"github.com/costa/polypod/internal/command"
)

// ---------------------------------------------------------------------------
// Package state
// ---------------------------------------------------------------------------

// debugToolCalls toggles verbose tracing of tool-call dispatch. The
// flag lives at package scope so the surrounding harness can read it
// directly when wiring is added; for now /debug-tool-call simply
// flips it. Mutex-guarded so concurrent CLI input is safe.
var (
	debugToolCallsMu sync.RWMutex
	debugToolCalls   bool
)

// DebugToolCallsEnabled exposes the current state of the toggle for
// other packages that may eventually consult it. Read with the lock
// held to avoid races with /debug-tool-call.
func DebugToolCallsEnabled() bool {
	debugToolCallsMu.RLock()
	defer debugToolCallsMu.RUnlock()
	return debugToolCalls
}

// setDebugToolCalls atomically updates the package flag and returns
// the new value.
func setDebugToolCalls(v bool) bool {
	debugToolCallsMu.Lock()
	defer debugToolCallsMu.Unlock()
	debugToolCalls = v
	return debugToolCalls
}

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

// RegisterBatch7Debug registers the six batch-7 debug commands on the
// supplied registry.
func RegisterBatch7Debug(reg *command.Registry) {
	reg.Register(cmdHeapdump())
	reg.Register(cmdPerfIssue())
	reg.Register(cmdDebugToolCall())
	reg.Register(cmdBreakCache())
	reg.Register(cmdMockLimits())
	reg.Register(cmdResetLimits())
}

// ---------------------------------------------------------------------------
// 1. /heapdump — write a heap profile to disk
// ---------------------------------------------------------------------------

func cmdHeapdump() *command.Command {
	return &command.Command{
		Name:        "heapdump",
		Description: "Gravar perfil de heap (pprof) em arquivo",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			// Resolve target path: explicit argument wins, otherwise
			// fall back to a timestamped file under the working dir.
			var target string
			if len(args) > 0 && args[0] != "" {
				target = args[0]
				if !filepath.IsAbs(target) {
					target = filepath.Join(ctx.WorkingDir, target)
				}
			} else {
				name := fmt.Sprintf("polypod-heap-%s.pprof",
					time.Now().UTC().Format("20060102-150405"))
				target = filepath.Join(ctx.WorkingDir, name)
			}

			// Make sure the parent directory exists; pprof.WriteHeapProfile
			// will not create it for us.
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return errResult(fmt.Sprintf("falha ao criar diretorio: %v", err)), nil
			}

			f, err := os.Create(target)
			if err != nil {
				return errResult(fmt.Sprintf("falha ao criar arquivo: %v", err)), nil
			}
			defer f.Close()

			// Force a GC pass first so the dump reflects live memory,
			// not what Go has not yet swept.
			runtime.GC()
			if err := pprof.WriteHeapProfile(f); err != nil {
				return errResult(fmt.Sprintf("falha ao escrever heap profile: %v", err)), nil
			}

			return okResult(fmt.Sprintf(
				"Heap profile gravado em %s\nUse 'go tool pprof %s' para inspecionar.",
				target, target)), nil
		},
	}
}

// ---------------------------------------------------------------------------
// 2. /perf-issue — dump runtime.MemStats and goroutine count
// ---------------------------------------------------------------------------

func cmdPerfIssue() *command.Command {
	return &command.Command{
		Name:        "perf-issue",
		Description: "Mostrar metricas de runtime (MemStats e goroutines)",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			var ms runtime.MemStats
			runtime.ReadMemStats(&ms)
			goroutines := runtime.NumGoroutine()

			// Render a compact, human-readable summary in Portuguese.
			// Sizes are reported in MiB to keep the output digestible.
			toMiB := func(b uint64) float64 {
				return float64(b) / (1024.0 * 1024.0)
			}

			out := fmt.Sprintf(
				"Diagnostico de desempenho\n"+
					"\n"+
					"Goroutines ativas : %d\n"+
					"GOMAXPROCS        : %d\n"+
					"NumCPU            : %d\n"+
					"\n"+
					"Memoria (MiB):\n"+
					"  Alloc           : %.2f\n"+
					"  TotalAlloc      : %.2f\n"+
					"  Sys             : %.2f\n"+
					"  HeapAlloc       : %.2f\n"+
					"  HeapSys         : %.2f\n"+
					"  HeapInuse       : %.2f\n"+
					"  HeapIdle        : %.2f\n"+
					"  StackInuse      : %.2f\n"+
					"\n"+
					"GC:\n"+
					"  NumGC           : %d\n"+
					"  NumForcedGC     : %d\n"+
					"  PauseTotalNs    : %d",
				goroutines,
				runtime.GOMAXPROCS(0),
				runtime.NumCPU(),
				toMiB(ms.Alloc),
				toMiB(ms.TotalAlloc),
				toMiB(ms.Sys),
				toMiB(ms.HeapAlloc),
				toMiB(ms.HeapSys),
				toMiB(ms.HeapInuse),
				toMiB(ms.HeapIdle),
				toMiB(ms.StackInuse),
				ms.NumGC,
				ms.NumForcedGC,
				ms.PauseTotalNs,
			)
			return okResult(out), nil
		},
	}
}

// ---------------------------------------------------------------------------
// 3. /debug-tool-call — toggle the debugToolCalls flag
// ---------------------------------------------------------------------------

func cmdDebugToolCall() *command.Command {
	return &command.Command{
		Name:        "debug-tool-call",
		Description: "Alternar tracing de chamadas de ferramentas",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			// Allow explicit on/off via argument, otherwise toggle.
			var newState bool
			if len(args) > 0 {
				switch args[0] {
				case "on", "true", "1", "yes", "sim":
					newState = true
				case "off", "false", "0", "no", "nao":
					newState = false
				default:
					return errResult(fmt.Sprintf(
						"valor invalido: %q (use on/off)", args[0])), nil
				}
				setDebugToolCalls(newState)
			} else {
				newState = !DebugToolCallsEnabled()
				setDebugToolCalls(newState)
			}

			if newState {
				return okResult("Debug de tool-calls: LIGADO"), nil
			}
			return okResult("Debug de tool-calls: DESLIGADO"), nil
		},
	}
}

// ---------------------------------------------------------------------------
// 4. /break-cache — placeholder until cache wiring lands
// ---------------------------------------------------------------------------

func cmdBreakCache() *command.Command {
	return &command.Command{
		Name:        "break-cache",
		Description: "Invalidar cache (em fila ate a integracao ser concluida)",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			return okResult("cache break queued (wiring pending)"), nil
		},
	}
}

// ---------------------------------------------------------------------------
// 5. /mock-limits — placeholder for the rate-limit mock toggle
// ---------------------------------------------------------------------------

func cmdMockLimits() *command.Command {
	return &command.Command{
		Name:        "mock-limits",
		Description: "Ativar limites simulados (placeholder)",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			return okResult("mock limits enabled"), nil
		},
	}
}

// ---------------------------------------------------------------------------
// 6. /reset-limits — placeholder for clearing rate-limit state
// ---------------------------------------------------------------------------

func cmdResetLimits() *command.Command {
	return &command.Command{
		Name:        "reset-limits",
		Description: "Resetar contadores de rate limit (placeholder)",
		Type:        command.CommandLocal,
		Source:      command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			return okResult("limits reset"), nil
		},
	}
}
