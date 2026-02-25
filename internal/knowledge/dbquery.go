package knowledge

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// SafeQuery represents a pre-defined, parameterized query.
type SafeQuery struct {
	Name        string
	Description string
	SQL         string
	ParamNames  []string
}

// DBQueryService executes pre-defined queries against the database.
type DBQueryService struct {
	pool    *pgxpool.Pool
	queries map[string]SafeQuery
	logger  *slog.Logger
}

// NewDBQueryService creates a new DB query service with pre-defined queries.
func NewDBQueryService(pool *pgxpool.Pool, logger *slog.Logger) *DBQueryService {
	svc := &DBQueryService{
		pool:    pool,
		queries: make(map[string]SafeQuery),
		logger:  logger,
	}
	svc.registerDefaults()
	return svc
}

func (s *DBQueryService) registerDefaults() {
	// Add pre-defined safe queries here.
	// These are templates for common business queries.
	// NEVER execute dynamic SQL - only parameterized pre-defined queries.
}

// RegisterQuery adds a new safe query.
func (s *DBQueryService) RegisterQuery(q SafeQuery) {
	s.queries[q.Name] = q
}

// Execute runs a pre-defined query by name with the given parameters.
func (s *DBQueryService) Execute(ctx context.Context, name string, params map[string]interface{}) ([]map[string]interface{}, error) {
	q, ok := s.queries[name]
	if !ok {
		return nil, fmt.Errorf("unknown query: %s", name)
	}

	args := make([]interface{}, len(q.ParamNames))
	for i, pn := range q.ParamNames {
		val, ok := params[pn]
		if !ok {
			return nil, fmt.Errorf("missing parameter: %s", pn)
		}
		args[i] = val
	}

	rows, err := s.pool.Query(ctx, q.SQL, args...)
	if err != nil {
		return nil, fmt.Errorf("executing query %s: %w", name, err)
	}
	defer rows.Close()

	descs := rows.FieldDescriptions()
	var results []map[string]interface{}

	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return nil, fmt.Errorf("reading row: %w", err)
		}

		row := make(map[string]interface{}, len(descs))
		for i, desc := range descs {
			row[string(desc.Name)] = vals[i]
		}
		results = append(results, row)
	}

	s.logger.Debug("query executed", "name", name, "rows", len(results))
	return results, nil
}

// ListQueries returns descriptions of all available queries.
func (s *DBQueryService) ListQueries() []string {
	var list []string
	for _, q := range s.queries {
		list = append(list, fmt.Sprintf("%s: %s", q.Name, q.Description))
	}
	return list
}

// FormatResults formats query results as a readable string.
func FormatResults(results []map[string]interface{}) string {
	if len(results) == 0 {
		return "Nenhum resultado encontrado."
	}

	var sb strings.Builder
	for i, row := range results {
		if i > 0 {
			sb.WriteString("\n---\n")
		}
		for k, v := range row {
			fmt.Fprintf(&sb, "%s: %v\n", k, v)
		}
	}
	return sb.String()
}
