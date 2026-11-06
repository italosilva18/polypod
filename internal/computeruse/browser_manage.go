package computeruse

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/costa/polypod/internal/browser"
	"github.com/costa/polypod/internal/types"
)

func registerBrowserManageTool(reg ToolRegistry, runtime *browserRuntime) {
	reg.RegisterDef(types.ToolDef{
		NameFn: func() string { return "browser_manage" },
		DescriptionFn: func() string {
			return "Gerencia navegador com perfil privado persistente: status, start, stop, tabs, new_tab, switch_tab, close_tab, navigate, snapshot, text e evaluate. Sem POLYPOD_BROWSER_CDP_URL, abre Chrome isolado sob demanda; com endpoint explícito, usa esse navegador. Avalie JavaScript apenas quando necessário e observe o efeito após ações."
		},
		InputSchemaFn: func() map[string]any {
			return map[string]any{"type": "object", "properties": map[string]any{
				"action":     map[string]any{"type": "string", "enum": []string{"status", "start", "stop", "tabs", "new_tab", "switch_tab", "activate_tab", "close_tab", "navigate", "snapshot", "text", "evaluate"}},
				"tab_id":     map[string]any{"type": "string"},
				"url":        map[string]any{"type": "string"},
				"javascript": map[string]any{"type": "string"},
			}, "required": []string{"action"}}
		},
		IsReadOnlyFn: func(args map[string]any) bool {
			switch stringArg(args, "action", "") {
			case "status", "tabs", "snapshot", "text":
				return true
			default:
				return false
			}
		},
		IsDestructiveFn: func(args map[string]any) bool {
			switch stringArg(args, "action", "") {
			case "status", "tabs", "snapshot", "text":
				return false
			default:
				return true
			}
		},
		ValidateFn: validateBrowserManage,
		ExecuteFn: func(ctx types.ToolContext, args map[string]any) (types.ToolResult, error) {
			result := executeBrowserManage(ctx, runtime, args)
			return result, nil
		},
	})
}

func validateBrowserManage(args map[string]any) types.ValidationResult {
	action := stringArg(args, "action", "")
	switch action {
	case "status", "start", "stop", "tabs", "snapshot", "text":
	case "new_tab":
		if raw := stringArg(args, "url", ""); raw != "" {
			if err := browser.ValidateNavigationURL(raw); err != nil {
				return types.ValidationResult{Message: err.Error()}
			}
		}
	case "switch_tab", "activate_tab", "close_tab":
		if stringArg(args, "tab_id", "") == "" {
			return types.ValidationResult{Message: "tab_id obrigatório"}
		}
	case "navigate":
		if err := browser.ValidateNavigationURL(stringArg(args, "url", "")); err != nil {
			return types.ValidationResult{Message: err.Error()}
		}
	case "evaluate":
		if stringArg(args, "tab_id", "") == "" || strings.TrimSpace(stringArg(args, "javascript", "")) == "" {
			return types.ValidationResult{Message: "tab_id e javascript são obrigatórios"}
		}
	default:
		return types.ValidationResult{Message: "ação de navegador desconhecida"}
	}
	return types.ValidationResult{Valid: true}
}

func executeBrowserManage(ctx types.ToolContext, runtime *browserRuntime, args map[string]any) types.ToolResult {
	action := stringArg(args, "action", "")
	if action == "status" {
		if runtime.endpoint != "" {
			tabs, err := (browser.CDP{Endpoint: runtime.endpoint}).Tabs(ctx.Context)
			return browserJSON(map[string]any{"mode": "attached", "connected": err == nil, "tabs": len(tabs)})
		}
		return browserJSON(runtime.managed.Status())
	}
	if action == "stop" {
		if runtime.endpoint != "" {
			return browserError(fmt.Errorf("não é possível encerrar Chrome conectado por endpoint externo"))
		}
		if err := runtime.managed.Stop(ctx.Context); err != nil {
			return browserError(err)
		}
		return browserJSON(runtime.managed.Status())
	}
	if action == "start" && runtime.endpoint != "" {
		return browserError(fmt.Errorf("Chrome externo já foi configurado; start requer perfil gerenciado"))
	}
	client, err := runtime.client(ctx.Context)
	if err != nil {
		return browserError(err)
	}
	if action == "start" {
		tabs, err := client.Tabs(ctx.Context)
		if err != nil {
			return browserError(err)
		}
		return browserJSON(map[string]any{"mode": "isolated_profile", "running": true, "headless": runtime.managed.Status().Headless, "tabs": len(tabs)})
	}
	return executeBrowserClient(ctx, client, action, args)
}

func executeBrowserClient(ctx types.ToolContext, client browser.CDP, action string, args map[string]any) types.ToolResult {
	id := stringArg(args, "tab_id", "")
	switch action {
	case "tabs":
		tabs, err := client.Tabs(ctx.Context)
		if err != nil {
			return browserError(err)
		}
		for i := range tabs {
			tabs[i].WebSocketURL = ""
		}
		return browserJSON(tabs)
	case "new_tab":
		tab, err := client.NewTab(ctx.Context)
		if err != nil {
			return browserError(err)
		}
		if rawURL := stringArg(args, "url", ""); rawURL != "" {
			result, err := client.Navigate(ctx.Context, tab.ID, rawURL)
			if err != nil {
				return browserError(fmt.Errorf("aba %s criada; %w", tab.ID, err))
			}
			out := browserActionResult(result)
			out.Content = browserJSON(map[string]any{"tab_id": tab.ID, "navigation": result}).Content
			return out
		}
		return browserJSON(map[string]any{"tab_id": tab.ID, "url": tab.URL})
	case "switch_tab", "activate_tab":
		if err := client.ActivateTab(ctx.Context, id); err != nil {
			return browserError(err)
		}
		return browserJSON(map[string]any{"active_tab_id": id})
	case "close_tab":
		if err := client.CloseTab(ctx.Context, id); err != nil {
			return browserError(err)
		}
		return browserJSON(map[string]any{"closed_tab_id": id})
	case "navigate":
		result, err := client.Navigate(ctx.Context, id, stringArg(args, "url", ""))
		if err != nil {
			if result != nil {
				return browserError(fmt.Errorf("%w; %s", err, result.Verification))
			}
			return browserError(err)
		}
		return browserActionResult(result)
	case "snapshot", "text":
		obs, err := client.Observe(ctx.Context, id)
		if err != nil {
			return browserError(err)
		}
		if action == "text" {
			return types.ToolResult{Content: obs.Text}
		}
		return browserObservationResult(obs)
	case "evaluate":
		value, err := client.Evaluate(ctx.Context, id, stringArg(args, "javascript", ""))
		if err != nil {
			return browserError(err)
		}
		return types.ToolResult{Content: string(value)}
	default:
		return browserError(fmt.Errorf("ação de navegador desconhecida"))
	}
}

func browserJSON(value any) types.ToolResult {
	data, err := json.Marshal(value)
	if err != nil {
		return browserError(err)
	}
	return types.ToolResult{Content: string(data)}
}

func browserError(err error) types.ToolResult {
	return types.ToolResult{IsError: true, Content: err.Error()}
}
