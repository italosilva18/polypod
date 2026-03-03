package knowledge

import (
	"context"
	"log/slog"
	"sync"
)

// Service orchestrates parallel search across multiple knowledge sources.
type Service struct {
	vector   vectorSearcher
	dbQuery  *DBQueryService
	apiProxy *APIProxy
	logger   *slog.Logger
}

// NewService creates a new knowledge service.
func NewService(vector vectorSearcher, dbQuery *DBQueryService, apiProxy *APIProxy, logger *slog.Logger) *Service {
	return &Service{
		vector:   vector,
		dbQuery:  dbQuery,
		apiProxy: apiProxy,
		logger:   logger,
	}
}

// Search performs parallel search across all configured sources.
func (s *Service) Search(ctx context.Context, query string) ([]string, error) {
	var (
		mu       sync.Mutex
		results  []string
		wg       sync.WaitGroup
	)

	// Vector search
	if s.vector != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fragments, err := s.vector.Search(ctx, query)
			if err != nil {
				s.logger.Warn("vector search failed", "error", err)
				return
			}
			mu.Lock()
			for _, f := range fragments {
				if f.Score > 0.3 { // minimum relevance threshold
					results = append(results, f.Content)
				}
			}
			mu.Unlock()
		}()
	}

	wg.Wait()

	s.logger.Debug("knowledge search completed", "sources_found", len(results))
	return results, nil
}
