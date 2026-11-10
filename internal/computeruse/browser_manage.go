package computeruse

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/costa/polypod/internal/browser"
	"github.com/costa/polypod/internal/types"
)

func registerBrowserManageTool(reg ToolRegistry, runtime *browserRuntime) {
	reg.RegisterDef(types.ToolDef{
		NameFn: func() string { return "browser_manage" },
		DescriptionFn: func() string {
			return "Gerencia Chrome/Chromium: status, extension_pair, attach, use_desktop, detach, start, stop, tabs, new_tab, switch_tab, close_tab, navigate, snapshot, text e evaluate. extension_pair conecta uma extensão instalada no navegador pessoal sem porta CDP; attach usa CDP local. Sem conexão externa, abre perfil privado sob demanda."
		},
		InputSchemaFn: func() map[string]any {
			return map[string]any{"type": "object", "properties": map[string]any{
				"action":      map[string]any{"type": "string", "enum": []string{"status", "extension_pair", "attach", "use_desktop", "detach", "start", "stop", "tabs", "new_tab", "switch_tab", "activate_tab", "close_tab", "navigate", "snapshot", "text", "evaluate"}},
				"endpoint":    map[string]any{"type": "string", "description": "Endpoint HTTP CDP local, por exemplo http://127.0.0.1:9222"},
				"profile_dir": map[string]any{"type": "string", "description": "Diretório absoluto do perfil de um navegador já aberto com CDP"},
				"tab_id":      map[string]any{"type": "string"},
				"url":         map[string]any{"type": "string"},
				"javascript":  map[string]any{"type": "string"},
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
	case "status", "extension_pair", "use_desktop", "detach", "start", "stop", "tabs", "snapshot", "text":
	case "attach":
		endpoint := strings.TrimSpace(stringArg(args, "endpoint", ""))
		profileDir := strings.TrimSpace(stringArg(args, "profile_dir", ""))
		if (endpoint == "") == (profileDir == "") {
			return types.ValidationResult{Message: "informe endpoint ou profile_dir para attach"}
		}
		if endpoint != "" {
			if _, err := browser.ValidateCDPEndpoint(endpoint); err != nil {
				return types.ValidationResult{Message: err.Error()}
			}
		} else if !filepath.IsAbs(profileDir) {
			return types.ValidationResult{Message: "profile_dir deve ser caminho absoluto"}
		}
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
		client, source, attached, err := runtime.external(ctx.Context)
		if attached {
			if source == "desktop" {
				return browserJSON(map[string]any{"mode": "desktop", "display_configured": IsDisplayAvailable()})
			}
			mode := "attached"
			if source == "extension" {
				mode = "extension"
			}
			state := map[string]any{"mode": mode, "source": source, "connected": false, "tabs": 0}
			if err == nil {
				var tabs []browser.Tab
				tabs, err = client.Tabs(ctx.Context)
				state["tabs"] = len(tabs)
				if source != "extension" {
					state["endpoint"] = client.Endpoint
				}
			}
			if err != nil {
				state["error"] = err.Error()
			} else {
				state["connected"] = true
			}
			return browserJSON(state)
		}
		return browserJSON(runtime.managed.Status())
	}
	if action == "extension_pair" {
		pairURL, connected, err := runtime.pairExtension()
		if err != nil {
			return browserError(err)
		}
		state := map[string]any{"mode": "extension", "connected": connected, "pair_url": pairURL}
		if wd, err := os.Getwd(); err == nil {
			path := filepath.Join(wd, "browser-extension")
			if _, err := os.Stat(filepath.Join(path, "manifest.json")); err == nil {
				state["extension_dir"] = path
			}
		}
		return browserJSON(state)
	}
	if action == "use_desktop" {
		if !IsDisplayAvailable() {
			return browserError(fmt.Errorf("nenhum desktop gráfico configurado para observar o navegador aberto"))
		}
		runtime.useDesktop()
		return browserJSON(map[string]any{"mode": "desktop", "display_configured": true})
	}
	if action == "attach" {
		client, tabs, err := runtime.attach(ctx.Context, strings.TrimSpace(stringArg(args, "endpoint", "")), strings.TrimSpace(stringArg(args, "profile_dir", "")))
		if err != nil {
			return browserError(fmt.Errorf("não foi possível conectar ao navegador aberto: %w; confira se ele iniciou com depuração CDP local", err))
		}
		return browserJSON(map[string]any{"mode": "attached", "connected": true, "endpoint": client.Endpoint, "tabs": len(tabs)})
	}
	if action == "detach" {
		runtime.detach()
		return executeBrowserManage(ctx, runtime, map[string]any{"action": "status"})
	}
	_, source, attached, _ := runtime.external(ctx.Context)
	if source == "desktop" && (action == "start" || action == "stop") {
		return browserError(fmt.Errorf("modo desktop ativo; use detach para voltar ao navegador gerenciado"))
	}
	if action == "stop" {
		if attached {
			return browserError(fmt.Errorf("não é possível encerrar Chrome conectado por endpoint externo"))
		}
		if err := runtime.managed.Stop(ctx.Context); err != nil {
			return browserError(err)
		}
		return browserJSON(runtime.managed.Status())
	}
	if action == "start" && attached {
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
