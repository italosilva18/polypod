package cli

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/costa/polypod/internal/permissions"
)

// approvalOption describes a single choice inside the approval modal.
type approvalOption struct {
	key     string // keyboard shortcut (single lower-case letter)
	label   string // human label shown in the modal
	allowed bool
	perm    bool
}

// approvalOptions is the canonical ordered list of choices. The
// approvalSelection field on model is an index into this slice.
var approvalOptions = []approvalOption{
	{key: "s", label: "Sim", allowed: true, perm: false},
	{key: "n", label: "Nao", allowed: false, perm: false},
	{key: "a", label: "Sempre permitir (para esta sessao)", allowed: true, perm: true},
	{key: "d", label: "Sempre negar (para esta sessao)", allowed: false, perm: true},
}

// handleApprovalKey processes a key press while the approval modal is
// showing. It returns the updated model and any command to run.
func (m model) handleApprovalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.currentApproval == nil {
		return m, nil
	}

	// Navigation: up/down moves the selection arrow.
	switch msg.Type {
	case tea.KeyUp:
		if m.approvalSelection > 0 {
			m.approvalSelection--
		}
		m.updateViewport()
		return m, nil
	case tea.KeyDown:
		if m.approvalSelection < len(approvalOptions)-1 {
			m.approvalSelection++
		}
		m.updateViewport()
		return m, nil
	case tea.KeyEsc:
		// Escape denies the request (one-shot).
		return m.sendApprovalDecision(approvalOption{key: "n", label: "Nao", allowed: false})
	case tea.KeyEnter:
		return m.sendApprovalDecision(approvalOptions[m.approvalSelection])
	}

	// Single-letter shortcuts: also accept "y" as an alias for "s" and
	// "y"/"n"/"a"/"d" as expected.
	switch strings.ToLower(msg.String()) {
	case "s", "y":
		return m.sendApprovalDecision(approvalOptions[0])
	case "n":
		return m.sendApprovalDecision(approvalOptions[1])
	case "a":
		return m.sendApprovalDecision(approvalOptions[2])
	case "d":
		return m.sendApprovalDecision(approvalOptions[3])
	}

	return m, nil
}

// sendApprovalDecision ships the user's choice back to the handler and
// clears the modal state.
func (m model) sendApprovalDecision(opt approvalOption) (tea.Model, tea.Cmd) {
	if m.currentApproval == nil || m.approvalHandler == nil {
		m.currentApproval = nil
		m.approvalSelection = 0
		m.updateViewport()
		return m, nil
	}
	id := m.currentApproval.id
	m.approvalHandler.SendResponse(id, permissions.ApprovalResponse{
		Allowed:   opt.allowed,
		Permanent: opt.perm,
	})
	m.currentApproval = nil
	m.approvalSelection = 0
	m.updateViewport()
	return m, nil
}

// renderApprovalModal returns the lipgloss-rendered modal overlay for
// the currently pending approval request. Callers should append the
// returned string to the View output (centered by the caller).
func (m model) renderApprovalModal() string {
	if m.currentApproval == nil {
		return ""
	}
	req := m.currentApproval.req

	// Pick an outer border style based on severity.
	box := approvalOverlayStyle
	riskBadgeStyle := approvalHighRiskStyle
	switch req.Risk.Level {
	case permissions.ToolRiskMedium:
		box = approvalOverlayMediumStyle
		riskBadgeStyle = approvalMediumRiskStyle
	case permissions.ToolRiskLow, permissions.ToolRiskSafe:
		box = approvalOverlayLowStyle
		riskBadgeStyle = approvalLowRiskStyle
	}

	var b strings.Builder

	// Title
	b.WriteString(approvalTitleStyle.Render("PERMISSAO REQUERIDA"))
	b.WriteString("\n")
	b.WriteString(separatorStyle.Render(strings.Repeat("─", 60)))
	b.WriteString("\n\n")

	// Tool + risk
	riskLabel := strings.ToUpper(req.Risk.Level.String())
	b.WriteString(approvalLabelStyle.Render("Tool:   "))
	b.WriteString(approvalValueStyle.Render(req.ToolName))
	b.WriteString("\n")
	b.WriteString(approvalLabelStyle.Render("Risk:   "))
	b.WriteString(riskBadgeStyle.Render(" " + riskLabel + " "))
	b.WriteString("\n")

	// Command (bash) or file (file tools)
	if cmd, ok := req.Args["command"].(string); ok && cmd != "" {
		if len(cmd) > 200 {
			cmd = cmd[:197] + "..."
		}
		b.WriteString("\n")
		b.WriteString(approvalLabelStyle.Render("Command:"))
		b.WriteString("\n  ")
		b.WriteString(approvalCommandStyle.Render(cmd))
		b.WriteString("\n")
	} else if p := extractApprovalFilePath(req.Args); p != "" {
		b.WriteString("\n")
		b.WriteString(approvalLabelStyle.Render("File:   "))
		b.WriteString(approvalValueStyle.Render(p))
		b.WriteString("\n")
	}

	// Explanation (optional)
	if req.Explanation != nil && req.Explanation.Explanation != "" {
		b.WriteString("\n")
		b.WriteString(approvalExplanationStyle.Render(req.Explanation.Explanation))
		b.WriteString("\n")
	} else if req.Reason != "" {
		b.WriteString("\n")
		b.WriteString(approvalExplanationStyle.Render(req.Reason))
		b.WriteString("\n")
	}

	// Options
	b.WriteString("\n")
	for i, opt := range approvalOptions {
		line := fmt.Sprintf("[%s] %s", opt.key, opt.label)
		if i == m.approvalSelection {
			b.WriteString(approvalSelectedStyle.Render("► "))
			b.WriteString(approvalSelectedStyle.Render(line))
		} else {
			b.WriteString("  ")
			b.WriteString(approvalUnselectedStyle.Render(line))
		}
		b.WriteString("\n")
	}

	// Footer hint
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("↑/↓ mover   Enter confirmar   Esc negar"))

	return box.Render(b.String())
}

// overlayApprovalModal centers the modal horizontally inside the full
// view area. Lipgloss's Place function is the cleanest way to do this.
func (m model) overlayApprovalModal(background string) string {
	if m.currentApproval == nil {
		return background
	}
	modal := m.renderApprovalModal()
	if m.width <= 0 || m.height <= 0 {
		return modal
	}
	// Place the modal in the middle of the full screen. lipgloss.Place
	// builds a blank canvas of the given size and positions the modal
	// there; we lose the background this way, which is fine for a
	// blocking modal that owns the screen.
	return lipgloss.Place(
		m.width, m.height,
		lipgloss.Center, lipgloss.Center,
		modal,
	)
}

// extractApprovalFilePath pulls a file path out of tool arguments for
// display in the approval modal. Matches the keys used elsewhere in
// the codebase.
func extractApprovalFilePath(args map[string]any) string {
	if args == nil {
		return ""
	}
	for _, key := range []string{"file_path", "path", "filePath", "filename"} {
		if v, ok := args[key]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}
