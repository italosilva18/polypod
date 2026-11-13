package cli

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
)

func TestSlashPaletteFiltersCommandsAsUserTypes(t *testing.T) {
	m := model{state: stateIdle, textarea: textarea.New(), width: 80, height: 24}
	m.textarea.SetValue("/pro")
	items := m.slashPaletteItems()
	if len(items) == 0 || items[0].value != "/provider" {
		t.Fatalf("/provider não apareceu primeiro: %+v", items)
	}
	if !strings.Contains(m.slashPaletteView(), "Escolhe provedor e modelo") {
		t.Fatal("menu não exibe a descrição")
	}
	m.paletteDismissedInput = "/pro"
	if got := m.slashPaletteItems(); len(got) != 0 {
		t.Fatalf("Esc não ocultou o menu: %+v", got)
	}
	m.textarea.SetValue("/prov")
	if got := m.slashPaletteItems(); len(got) == 0 {
		t.Fatal("menu não voltou após digitar")
	}
	m.paletteDismissedInput = ""
	first, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = first.(model)
	if len(m.slashPaletteItems()) != 0 {
		t.Fatal("Esc não ocultou as opções")
	}
	second, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = second.(model)
	m.textarea.SetValue("/prov")
	if len(m.slashPaletteItems()) == 0 {
		t.Fatal("menu não voltou após limpar a entrada")
	}
}

func TestSlashPaletteProviderAndModelSelection(t *testing.T) {
	var selectedProvider, selectedModel string
	m := model{state: stateIdle, textarea: textarea.New(), width: 80, height: 24,
		cmdDeps: commandDeps{
			listProviders: func() []ProviderOption {
				return []ProviderOption{{Name: "chatgpt", Model: "gpt-6.1-sol", Available: true}, {Name: "nvidia", Model: "nvidia/test-model", Available: true}}
			},
			switchProvider: func(provider, model string) error { selectedProvider, selectedModel = provider, model; return nil },
		},
	}
	m.textarea.SetValue("/provider n")
	items := m.slashPaletteItems()
	if len(items) != 1 || items[0].provider != "nvidia" {
		t.Fatalf("provedores filtrados: %+v", items)
	}
	if handled, _ := m.acceptSlashPalette(true); !handled {
		t.Fatal("provedor não selecionado")
	}
	if got := m.textarea.Value(); got != "/provider nvidia " {
		t.Fatalf("entrada após selecionar provedor: %q", got)
	}
	items = m.slashPaletteItems()
	if len(items) != 1 || items[0].model != "nvidia/test-model" {
		t.Fatalf("modelos filtrados: %+v", items)
	}
	if handled, _ := m.acceptSlashPalette(true); !handled {
		t.Fatal("modelo não selecionado")
	}
	if selectedProvider != "nvidia" || selectedModel != "nvidia/test-model" {
		t.Fatalf("troca incorreta: %s/%s", selectedProvider, selectedModel)
	}
	if got := m.textarea.Value(); got != "" {
		t.Fatalf("entrada não limpa: %q", got)
	}
}

func TestProviderCommandAcceptsTypedSelection(t *testing.T) {
	var provider, model string
	result := processCommand("/provider nvidia nvidia/test-model", commandDeps{
		switchProvider: func(p, m string) error { provider, model = p, m; return nil },
	}, nil)
	if !result.handled || provider != "nvidia" || model != "nvidia/test-model" {
		t.Fatalf("comando não aplicou a seleção: %+v, %s/%s", result, provider, model)
	}
}

func TestSlashPaletteIncludesSkillsAndCommandShortcut(t *testing.T) {
	m := model{state: stateIdle, textarea: textarea.New(), width: 80, height: 24,
		cmdDeps: commandDeps{listSkills: func() []string { return []string{"code_review"} }}}
	m.textarea.SetValue("/code_")
	items := m.slashPaletteItems()
	if len(items) != 1 || items[0].kind != "skill" || items[0].value != "/code_review:skill" {
		t.Fatalf("skill não encontrada: %+v", items)
	}
	if handled, _ := m.acceptSlashPalette(true); !handled {
		t.Fatal("seleção da skill não tratada")
	}
	if got := m.textarea.Value(); got != "Use a skill code_review para " {
		t.Fatalf("rascunho da skill: %q", got)
	}
	m.textarea.SetValue("outro")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlK})
	if got := next.(model).textarea.Value(); got != "/" {
		t.Fatalf("Ctrl+K não abriu a paleta: %q", got)
	}
	next, _ = next.(model).Update(tea.KeyMsg{Type: tea.KeyEsc})
	if got := next.(model).textarea.Value(); got != "outro" {
		t.Fatalf("Esc perdeu o rascunho: %q", got)
	}
}
