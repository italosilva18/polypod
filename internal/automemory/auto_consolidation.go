// Package automemory — Auto-consolidação simples
package automemory

import (
	"context"
	"log/slog"
	"time"
)

// Consolidator gerencia auto-consolidação
type Consolidator struct {
	logger *slog.Logger
}

// NewConsolidator cria novo consolidator
func NewConsolidator(logger *slog.Logger) *Consolidator {
	return &Consolidator{logger: logger}
}

// Start inicia auto-consolidação periódica
func (c *Consolidator) Start(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				c.logger.Info("auto-consolidation: running")
			}
		}
	}()
	
	c.logger.Info("auto-consolidation: started", "interval", interval)
}
