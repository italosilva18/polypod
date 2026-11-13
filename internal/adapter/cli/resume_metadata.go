package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/costa/polypod/internal/session"
)

// currentSessionMetadata records the settings that affect the next AI turn.
// Existing metadata, including the original project root and user title, is
// retained when a branch is saved or the process exits.
func (a *Adapter) currentSessionMetadata(existing session.SessionMetadata) session.SessionMetadata {
	metadata := existing
	if metadata.Tag == "" {
		metadata.Tag = "cli"
	}
	if metadata.ProjectRoot == "" {
		metadata.ProjectRoot, _ = os.Getwd()
	}
	if a.aiSvc != nil {
		metadata.AgentName = a.aiSvc.ActiveAgent()
		metadata.Provider = a.aiSvc.CurrentProvider()
		metadata.Model = a.aiSvc.CurrentModel()
	}
	if a.modeState != nil {
		metadata.Mode = string(a.modeState.Current)
	}
	return metadata
}

// restoreSessionMetadata reactivates available agent and mode settings. A
// session from another project is surfaced to the user: changing the process
// working directory here would also redirect unrelated remote channels.
func (a *Adapter) restoreSessionMetadata(metadata session.SessionMetadata) []string {
	var warnings []string
	providerName, modelName := metadata.Provider, metadata.Model
	if providerName == "" && a.cfg != nil {
		providerName, modelName = a.cfg.AI.Provider, a.cfg.AI.Model
	}
	if providerName != "" && modelName != "" && a.aiSvc != nil {
		if err := a.switchProviderRuntime(providerName, modelName); err != nil {
			warnings = append(warnings, fmt.Sprintf("provedor/modelo da sessão indisponível: %v", err))
		}
	}
	if metadata.AgentName != "" && a.aiSvc != nil {
		if a.agents != nil && a.agents.Has(metadata.AgentName) {
			a.aiSvc.SetAgent(metadata.AgentName)
		} else {
			warnings = append(warnings, fmt.Sprintf("agente %q nao encontrado; agente atual mantido", metadata.AgentName))
		}
	}
	if metadata.Mode != "" && metadata.Mode != "interactive" && a.modeState != nil {
		if _, err := a.modeState.SetMode(metadata.Mode); err != nil {
			warnings = append(warnings, fmt.Sprintf("modo %q nao disponivel; modo atual mantido", metadata.Mode))
		} else if a.permMgr != nil {
			a.permMgr.SetModeBlocked(a.modeState.BlockedTools())
		}
	}
	if metadata.ProjectRoot != "" {
		cwd, err := os.Getwd()
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("raiz da sessao: %s; nao foi possivel verificar o diretorio atual", metadata.ProjectRoot))
		} else if normalizedPath(cwd) != normalizedPath(metadata.ProjectRoot) {
			warnings = append(warnings, fmt.Sprintf("sessao de %s; arquivos e ferramentas continuam em %s. Abra o Polypod na raiz da sessao para trabalhar naquele projeto", metadata.ProjectRoot, cwd))
		}
	}
	return warnings
}

func normalizedPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return filepath.Clean(resolved)
	}
	return filepath.Clean(abs)
}
