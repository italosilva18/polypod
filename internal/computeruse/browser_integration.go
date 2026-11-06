package computeruse

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/costa/polypod/internal/browser"
	"github.com/costa/polypod/internal/types"
)

func registerBrowserTools(reg ToolRegistry, cu *ComputerUseTool) {
	runtime := newBrowserRuntime()
	reg.RegisterDef(types.ToolDef{
		NameFn: func() string { return "browser_observe" },
		DescriptionFn: func() string {
			return "Observa abas e elementos DOM do Chrome com perfil privado ou CDP local, com seletores e imagem. Sem navegador, usa screenshot/OCR desktop. Use tab_id quando houver várias abas. Coordenadas DOM são do viewport, não da tela."
		},
		InputSchemaFn: func() map[string]any {
			return map[string]any{"type": "object", "properties": map[string]any{"tab_id": map[string]any{"type": "string"}}}
		},
		IsReadOnlyFn: func(map[string]any) bool { return true },
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			client, err := runtime.client(ctx.Context)
			if err != nil {
				return browserDesktopFallback(ctx, cu, err), nil
			}
			tabs, err := client.Tabs(ctx.Context)
			if err != nil {
				return browserDesktopFallback(ctx, cu, err), nil
			}
			if len(tabs) == 0 {
				return browserDesktopFallback(ctx, cu, fmt.Errorf("nenhuma aba CDP disponível")), nil
			}

			id := stringArg(args, "tab_id", "")
			if id == "" && len(tabs) != 1 {
				for i := range tabs {
					tabs[i].WebSocketURL = ""
				}
				data, _ := json.Marshal(tabs)
				return types.ToolResult{Content: "Selecione tab_id para observar uma aba: " + string(data)}, nil
			}
			obs, err := client.Observe(ctx.Context, id)
			if err != nil {
				return browserDesktopFallback(ctx, cu, err), nil
			}
			return browserObservationResult(obs), nil
		},
	})
	reg.RegisterDef(types.ToolDef{
		NameFn: func() string { return "browser_act" },
		DescriptionFn: func() string {
			return "Clica ou preenche um elemento DOM por seletor único obtido em browser_observe; rejeita alvos ocultos, encobertos ou desabilitados. Sempre observa depois. expected_text/expected_url verificam o efeito. Não repita automaticamente ações com resultado desconhecido; use browser_observe ou computer_screenshot e ferramentas desktop."
		},
		InputSchemaFn: func() map[string]any {
			return map[string]any{"type": "object", "properties": map[string]any{
				"tab_id": map[string]any{"type": "string"}, "action": map[string]any{"type": "string", "enum": []string{"click", "fill"}}, "selector": map[string]any{"type": "string"}, "text": map[string]any{"type": "string"}, "expected_text": map[string]any{"type": "string"}, "expected_url": map[string]any{"type": "string"}}, "required": []string{"tab_id", "action", "selector"}}
		},
		IsDestructiveFn: func(map[string]any) bool { return true },
		ValidateFn: func(args map[string]any) types.ValidationResult {
			if stringArg(args, "tab_id", "") == "" || stringArg(args, "selector", "") == "" {
				return types.ValidationResult{Message: "tab_id e selector são obrigatórios"}
			}
			action := stringArg(args, "action", "")
			if action != "click" && action != "fill" {
				return types.ValidationResult{Message: "action deve ser click ou fill"}
			}
			if action == "fill" {
				if _, ok := args["text"].(string); !ok {
					return types.ValidationResult{Message: "text é obrigatório para fill (use string vazia para limpar)"}
				}
			}
			return types.ValidationResult{Valid: true}
		},
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			client, err := runtime.client(ctx.Context)
			if err != nil {
				return types.ToolResult{IsError: true, Content: err.Error()}, nil
			}
			result, err := client.Act(ctx.Context, browser.Action{TabID: stringArg(args, "tab_id", ""), Kind: stringArg(args, "action", ""), Selector: stringArg(args, "selector", ""), Text: stringArg(args, "text", ""), ExpectedText: stringArg(args, "expected_text", ""), ExpectedURL: stringArg(args, "expected_url", "")})
			if err != nil {
				message := err.Error()
				if result != nil {
					message += "\n" + result.Verification
				}
				return types.ToolResult{IsError: true, Content: message}, nil
			}
			data, _ := json.Marshal(result)
			output := types.ToolResult{Content: string(data)}
			if result.Observation != nil && result.Observation.Screenshot != "" {
				output.Images = []types.ToolImage{{MIMEType: "image/png", Base64: result.Observation.Screenshot}}
			}
			return output, nil
		},
	})
	registerBrowserManageTool(reg, runtime)
}

type browserRuntime struct {
	endpoint string
	managed  *browser.ManagedBrowser
}

func newBrowserRuntime() *browserRuntime {
	headless := !IsDisplayAvailable()
	if raw := strings.TrimSpace(os.Getenv("POLYPOD_BROWSER_HEADLESS")); raw != "" {
		headless = raw != "0" && !strings.EqualFold(raw, "false")
	}
	return &browserRuntime{
		endpoint: strings.TrimSpace(os.Getenv("POLYPOD_BROWSER_CDP_URL")),
		managed: browser.NewManagedBrowser(browser.ManagedConfig{
			Binary: os.Getenv("POLYPOD_BROWSER_BINARY"), ProfileDir: os.Getenv("POLYPOD_BROWSER_PROFILE_DIR"),
			Headless: headless, FallbackHeadless: !headless,
			DesktopUser: os.Getenv("POLYPOD_DESKTOP_USER"), DesktopHome: os.Getenv("POLYPOD_DESKTOP_HOME"),
		}),
	}
}

func (r *browserRuntime) client(ctx context.Context) (browser.CDP, error) {
	if r.endpoint != "" {
		return browser.CDP{Endpoint: r.endpoint}, nil
	}
	return r.managed.Start(ctx)
}

func browserActionResult(result *browser.ActionResult) types.ToolResult {
	data, _ := json.Marshal(result)
	out := types.ToolResult{Content: string(data)}
	if result.Observation != nil && result.Observation.Screenshot != "" {
		out.Images = []types.ToolImage{{MIMEType: "image/png", Base64: result.Observation.Screenshot}}
	}
	return out
}
func browserObservationResult(obs *browser.Observation) types.ToolResult {
	data, _ := json.Marshal(obs)
	result := types.ToolResult{Content: string(data)}
	if obs.Screenshot != "" {
		result.Images = []types.ToolImage{{MIMEType: "image/png", Base64: obs.Screenshot}}
	}
	return result
}

func browserDesktopFallback(ctx types.ToolContext, cu *ComputerUseTool, cause error) types.ToolResult {
	if !IsDisplayAvailable() {
		return types.ToolResult{IsError: true, Content: cause.Error() + ". Configure CDP local com --remote-allow-origins=http://localhost ou disponibilize um desktop para screenshot/OCR."}
	}
	result, err := cu.ExecuteAction(ctx.Context, ToolAction{Action: "screenshot"})
	if err != nil {
		return types.ToolResult{IsError: true, Content: err.Error()}
	}
	output := toolResultFromCU(result)
	output.Content = "DOM indisponível: " + cause.Error() + "\nFallback desktop (coordenadas de tela):\n" + output.Content
	return output
}
