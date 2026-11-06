package openapitools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/costa/polypod/internal/skill"
)

// sampleSpec is a minimal but representative OpenAPI JSON spec used across tests.
const sampleSpec = `{
  "openapi": "3.0.0",
  "info": {"title": "Sample", "version": "1.0"},
  "paths": {
    "/users": {
      "get": {
        "operationId": "listUsers",
        "summary": "List users",
        "description": "Returns all users",
        "parameters": [
          {
            "name": "limit",
            "in": "query",
            "description": "Max results",
            "required": false,
            "schema": {"type": "integer"}
          },
          {
            "name": "X-Token",
            "in": "header",
            "description": "Auth token",
            "required": true,
            "schema": {"type": "string"}
          }
        ]
      },
      "post": {
        "summary": "Create user"
      }
    },
    "/users/{id}": {
      "parameters": [
        {"name": "id", "in": "path", "required": true, "schema": {"type": "string"}}
      ],
      "get": {
        "operationId": "getUser",
        "summary": "Get user by id"
      }
    }
  }
}`

func findEndpoint(eps []APIEndpoint, opID string) *APIEndpoint {
	for i := range eps {
		if eps[i].OperationID == opID {
			return &eps[i]
		}
	}
	return nil
}

func TestParseOpenAPI_Basic(t *testing.T) {
	eps := parseOpenAPI([]byte(sampleSpec))
	if len(eps) == 0 {
		t.Fatal("expected endpoints, got none")
	}
	// /users GET + POST and /users/{id} GET = 3 endpoints.
	if len(eps) != 3 {
		t.Errorf("expected 3 endpoints, got %d", len(eps))
	}

	ep := findEndpoint(eps, "listUsers")
	if ep == nil {
		t.Fatal("listUsers endpoint not found")
	}
	if ep.Method != "get" {
		t.Errorf("expected method 'get', got %q", ep.Method)
	}
	if ep.Path != "/users" {
		t.Errorf("expected path '/users', got %q", ep.Path)
	}
	if ep.Summary != "List users" {
		t.Errorf("expected summary 'List users', got %q", ep.Summary)
	}
	if ep.Description != "Returns all users" {
		t.Errorf("expected description to be set, got %q", ep.Description)
	}
}

func TestParseOpenAPI_Parameters(t *testing.T) {
	eps := parseOpenAPI([]byte(sampleSpec))
	ep := findEndpoint(eps, "listUsers")
	if ep == nil {
		t.Fatal("listUsers endpoint not found")
	}
	if len(ep.Parameters) != 2 {
		t.Fatalf("expected 2 parameters, got %d", len(ep.Parameters))
	}

	var limit, token *APIParam
	for i := range ep.Parameters {
		switch ep.Parameters[i].Name {
		case "limit":
			limit = &ep.Parameters[i]
		case "X-Token":
			token = &ep.Parameters[i]
		}
	}
	if limit == nil {
		t.Fatal("limit parameter not found")
	}
	if limit.In != "query" {
		t.Errorf("limit.In = %q, want 'query'", limit.In)
	}
	if limit.Type != "integer" {
		t.Errorf("limit.Type = %q, want 'integer'", limit.Type)
	}
	if limit.Required {
		t.Error("limit should not be required")
	}
	if limit.Description != "Max results" {
		t.Errorf("limit.Description = %q", limit.Description)
	}

	if token == nil {
		t.Fatal("X-Token parameter not found")
	}
	if token.In != "header" {
		t.Errorf("token.In = %q, want 'header'", token.In)
	}
	if !token.Required {
		t.Error("X-Token should be required")
	}
	if token.Type != "string" {
		t.Errorf("token.Type = %q, want 'string'", token.Type)
	}
}

func TestParseOpenAPI_OperationIDFallback(t *testing.T) {
	// Operation lacking operationId -> fallback "<method>_<path with / replaced>".
	eps := parseOpenAPI([]byte(sampleSpec))

	// POST /users has no operationId in the spec.
	var found *APIEndpoint
	for i := range eps {
		if eps[i].Method == "post" && eps[i].Path == "/users" {
			found = &eps[i]
			break
		}
	}
	if found == nil {
		t.Fatal("POST /users endpoint not found")
	}
	want := "post_users"
	if found.OperationID != want {
		t.Errorf("fallback OperationID = %q, want %q", found.OperationID, want)
	}
}

func TestParseOpenAPI_SkipsPathLevelParameters(t *testing.T) {
	// The path /users/{id} has a top-level "parameters" key that must NOT
	// be treated as an HTTP method/operation.
	eps := parseOpenAPI([]byte(sampleSpec))

	for _, ep := range eps {
		if ep.Method == "parameters" {
			t.Errorf("parameters was incorrectly treated as an operation: %+v", ep)
		}
	}

	// Confirm getUser was parsed and no spurious 4th endpoint exists.
	if ep := findEndpoint(eps, "getUser"); ep == nil {
		t.Error("getUser endpoint not found")
	}
}

func TestParseOpenAPI_InvalidJSON(t *testing.T) {
	if eps := parseOpenAPI([]byte("not-valid-json")); eps != nil {
		t.Errorf("expected nil for invalid JSON, got %+v", eps)
	}
}

func TestParseOpenAPI_NoPaths(t *testing.T) {
	// Valid JSON but missing "paths" key.
	eps := parseOpenAPI([]byte(`{"openapi":"3.0.0","info":{"title":"X","version":"1"}}`))
	if eps != nil {
		t.Errorf("expected nil for spec without paths, got %+v", eps)
	}
}

func TestParseOpenAPI_EmptyPaths(t *testing.T) {
	eps := parseOpenAPI([]byte(`{"paths":{}}`))
	if len(eps) != 0 {
		t.Errorf("expected 0 endpoints for empty paths, got %d", len(eps))
	}
}

func TestGetString(t *testing.T) {
	m := map[string]any{
		"name":   "hello",
		"count":  42,
		"flag":   true,
		"nested": map[string]any{"x": "y"},
	}
	if got := getString(m, "name"); got != "hello" {
		t.Errorf("getString(name) = %q, want 'hello'", got)
	}
	if got := getString(m, "missing"); got != "" {
		t.Errorf("getString(missing) = %q, want empty", got)
	}
	// Non-string values should return empty string.
	if got := getString(m, "count"); got != "" {
		t.Errorf("getString(count) = %q, want empty (not a string)", got)
	}
	if got := getString(m, "flag"); got != "" {
		t.Errorf("getString(flag) = %q, want empty (not a string)", got)
	}
	if got := getString(m, "nested"); got != "" {
		t.Errorf("getString(nested) = %q, want empty (not a string)", got)
	}
}

func TestRegisterSkills(t *testing.T) {
	// Registry's skills map is unexported, so use the real constructor;
	// we just check that openapi_load gets added to whatever builtins exist.
	reg := skill.NewRegistry()
	before := len(reg.List())
	RegisterSkills(reg)
	after := len(reg.List())
	if after != before+1 {
		t.Errorf("expected registry size to grow by 1 (got %d -> %d)", before, after)
	}

	s, ok := reg.Get("openapi_load")
	if !ok {
		t.Fatal("openapi_load skill not registered")
	}
	if s.Name != "openapi_load" {
		t.Errorf("skill.Name = %q, want 'openapi_load'", s.Name)
	}
	if s.Description == "" {
		t.Error("skill.Description should not be empty")
	}
	if s.Execute == nil {
		t.Error("skill.Execute should not be nil")
	}
	// Verify that "source" is declared as a required parameter.
	foundSource := false
	for _, r := range s.Parameters.Required {
		if r == "source" {
			foundSource = true
			break
		}
	}
	if !foundSource {
		t.Error("'source' should be listed as a required parameter")
	}
	if _, ok := s.Parameters.Properties["source"]; !ok {
		t.Error("'source' property should be declared in Parameters")
	}
}

func TestOpenAPILoad_FromFile(t *testing.T) {
	// Write spec to temp dir, chdir there so the skill can persist its
	// side-effect file (.polypod/tools/openapi_endpoints.json) without
	// polluting the repo.
	dir := t.TempDir()
	specPath := filepath.Join(dir, "spec.json")
	if err := os.WriteFile(specPath, []byte(sampleSpec), 0644); err != nil {
		t.Fatalf("writing spec: %v", err)
	}

	origWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(origWD)

	reg := skill.NewRegistry()
	RegisterSkills(reg)
	s, ok := reg.Get("openapi_load")
	if !ok {
		t.Fatal("openapi_load not registered")
	}

	out, err := s.Execute(map[string]string{"source": specPath})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !strings.Contains(out, "3 endpoints carregados") {
		t.Errorf("output should mention 3 endpoints, got: %s", out)
	}
	if !strings.Contains(out, "listUsers") {
		t.Errorf("output should mention listUsers, got: %s", out)
	}
	if !strings.Contains(out, "GET") {
		t.Errorf("output should mention GET method, got: %s", out)
	}

	// Verify side-effect file was written and contains endpoints.
	savedPath := filepath.Join(dir, ".polypod", "tools", "openapi_endpoints.json")
	data, err := os.ReadFile(savedPath)
	if err != nil {
		t.Fatalf("reading saved endpoints: %v", err)
	}
	var saved []APIEndpoint
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatalf("unmarshal saved endpoints: %v", err)
	}
	if len(saved) != 3 {
		t.Errorf("saved endpoints count = %d, want 3", len(saved))
	}
}

func TestOpenAPILoad_FileNotFound(t *testing.T) {
	dir := t.TempDir()
	origWD, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(origWD)

	reg := skill.NewRegistry()
	RegisterSkills(reg)
	s, _ := reg.Get("openapi_load")

	_, err := s.Execute(map[string]string{"source": filepath.Join(dir, "does-not-exist.json")})
	if err == nil {
		t.Error("expected error when file does not exist, got nil")
	}
	if err != nil && !strings.Contains(err.Error(), "lendo spec") {
		t.Errorf("error should mention 'lendo spec', got: %v", err)
	}
}

func TestOpenAPILoad_EmptySpec(t *testing.T) {
	dir := t.TempDir()
	specPath := filepath.Join(dir, "empty.json")
	// Valid JSON, but no endpoints extractable.
	if err := os.WriteFile(specPath, []byte(`{"paths":{}}`), 0644); err != nil {
		t.Fatalf("writing spec: %v", err)
	}

	origWD, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(origWD)

	reg := skill.NewRegistry()
	RegisterSkills(reg)
	s, _ := reg.Get("openapi_load")

	out, err := s.Execute(map[string]string{"source": specPath})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Nenhum endpoint") {
		t.Errorf("expected 'Nenhum endpoint' message, got: %s", out)
	}
}
