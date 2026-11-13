package cli

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestSideQuestionUsesContextWithoutChangingConversation(t *testing.T) {
	called := ""
	m := model{width: 80, height: 24,
		messages: []chatEntry{{role: "user", content: "O teste falhou na linha 12"}},
		cmdDeps: commandDeps{generateSideText: func(prompt string) (string, error) {
			called = prompt
			return "Confira a entrada da linha 12.", nil
		}},
	}
	cmd := m.askSideQuestion("Qual é o próximo passo?")
	if cmd == nil || m.sideAnswer != "Consultando..." {
		t.Fatal("pergunta rápida não iniciada")
	}
	msg := cmd()
	next, _ := m.Update(msg)
	m = next.(model)
	if !strings.Contains(called, "O teste falhou na linha 12") || !strings.Contains(m.sideAnswer, "Confira") {
		t.Fatalf("contexto ou resposta incorretos: %q / %q", called, m.sideAnswer)
	}
	if len(m.messages) != 1 {
		t.Fatalf("pergunta alterou a conversa: %+v", m.messages)
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if next.(model).sideAnswer != "" {
		t.Fatal("Esc não fechou a pergunta rápida")
	}
}
