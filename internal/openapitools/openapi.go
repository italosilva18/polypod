package openapitools

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/costa/polypod/internal/skill"
	"github.com/sashabaranov/go-openai/jsonschema"
)

// APIEndpoint represents a parsed OpenAPI operation.
type APIEndpoint struct {
	OperationID string
	Method      string
	Path        string
	Summary     string
	Description string
	Parameters  []APIParam
}

// APIParam is an API parameter.
type APIParam struct {
	Name        string
	In          string // "query", "path", "header", "body"
	Type        string
	Description string
	Required    bool
}

// RegisterSkills registers OpenAPI-to-tools skills.
func RegisterSkills(reg *skill.Registry) {
	reg.Register(&skill.Skill{
		Name:        "openapi_load",
		Description: "Carregar spec OpenAPI/Swagger e gerar ferramentas automaticamente",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"source": {Type: jsonschema.String, Description: "URL ou caminho do arquivo OpenAPI (JSON/YAML)"},
			},
			Required: []string{"source"},
		},
		Execute: func(args map[string]string) (string, error) {
			source := args["source"]
			var data []byte
			var err error

			if strings.HasPrefix(source, "http") {
				client := &http.Client{Timeout: 15 * time.Second}
				resp, err := client.Get(source)
				if err != nil {
					return "", fmt.Errorf("baixando spec: %w", err)
				}
				defer resp.Body.Close()
				data, err = io.ReadAll(resp.Body)
				if err != nil {
					return "", err
				}
			} else {
				data, err = os.ReadFile(source)
				if err != nil {
					return "", fmt.Errorf("lendo spec: %w", err)
				}
			}

			endpoints := parseOpenAPI(data)
			if len(endpoints) == 0 {
				return "Nenhum endpoint encontrado na spec.", nil
			}

			var sb strings.Builder
			fmt.Fprintf(&sb, "## OpenAPI: %d endpoints carregados\n\n", len(endpoints))
			for _, ep := range endpoints {
				fmt.Fprintf(&sb, "- **%s** `%s %s`\n", ep.OperationID, strings.ToUpper(ep.Method), ep.Path)
				if ep.Summary != "" {
					fmt.Fprintf(&sb, "  %s\n", ep.Summary)
				}
				for _, p := range ep.Parameters {
					req := ""
					if p.Required {
						req = " (obrigatorio)"
					}
					fmt.Fprintf(&sb, "  - `%s` (%s, %s)%s: %s\n", p.Name, p.In, p.Type, req, p.Description)
				}
			}

			// Save parsed endpoints for reference
			os.MkdirAll(".polypod/tools", 0755)
			epJSON, _ := json.MarshalIndent(endpoints, "", "  ")
			os.WriteFile(".polypod/tools/openapi_endpoints.json", epJSON, 0644)

			sb.WriteString("\nEndpoints salvos em .polypod/tools/openapi_endpoints.json\n")
			sb.WriteString("Use os endpoints como referencia para chamar a API via fetch_url ou run_command (curl).\n")

			return sb.String(), nil
		},
	})
}

func parseOpenAPI(data []byte) []APIEndpoint {
	var spec map[string]any
	if err := json.Unmarshal(data, &spec); err != nil {
		return nil
	}

	var endpoints []APIEndpoint

	paths, ok := spec["paths"].(map[string]any)
	if !ok {
		return nil
	}

	for path, methods := range paths {
		methodMap, ok := methods.(map[string]any)
		if !ok {
			continue
		}
		for method, details := range methodMap {
			if method == "parameters" {
				continue
			}
			detailMap, ok := details.(map[string]any)
			if !ok {
				continue
			}

			ep := APIEndpoint{
				Method: method,
				Path:   path,
			}
			if opID, ok := detailMap["operationId"].(string); ok {
				ep.OperationID = opID
			} else {
				ep.OperationID = method + "_" + strings.ReplaceAll(strings.Trim(path, "/"), "/", "_")
			}
			if summary, ok := detailMap["summary"].(string); ok {
				ep.Summary = summary
			}
			if desc, ok := detailMap["description"].(string); ok {
				ep.Description = desc
			}

			if params, ok := detailMap["parameters"].([]any); ok {
				for _, p := range params {
					pm, ok := p.(map[string]any)
					if !ok {
						continue
					}
					param := APIParam{
						Name: getString(pm, "name"),
						In:   getString(pm, "in"),
						Description: getString(pm, "description"),
					}
					if req, ok := pm["required"].(bool); ok {
						param.Required = req
					}
					if schema, ok := pm["schema"].(map[string]any); ok {
						param.Type = getString(schema, "type")
					}
					ep.Parameters = append(ep.Parameters, param)
				}
			}

			endpoints = append(endpoints, ep)
		}
	}

	return endpoints
}

func getString(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}
