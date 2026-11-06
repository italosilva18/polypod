// Package approval provides interactive approval handlers for the permission
// enforcer. It includes a CLI handler (terminal prompt with colored output)
// and a headless handler (policy-based auto-decisions).
package approval

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/costa/polypod/internal/permissions"
)

// -------------------------------------------------------------------
// CLIApprovalHandler — interactive terminal approval prompt
// -------------------------------------------------------------------

// CLIApprovalHandler implements the permissions.AskApprovalFn contract
// by printing a formatted prompt to the terminal and reading user input.
type CLIApprovalHandler struct {
	reader  io.Reader
	writer  io.Writer
	timeout time.Duration
	yolo    bool // auto-approve everything (YOLO mode)
}

// CLIOption configures a CLIApprovalHandler.
type CLIOption func(*CLIApprovalHandler)

// WithReader overrides stdin (useful for testing).
func WithReader(r io.Reader) CLIOption {
	return func(h *CLIApprovalHandler) { h.reader = r }
}

// WithWriter overrides stdout (useful for testing).
func WithWriter(w io.Writer) CLIOption {
	return func(h *CLIApprovalHandler) { h.writer = w }
}

// WithTimeout sets the approval timeout. Zero means no timeout.
func WithTimeout(d time.Duration) CLIOption {
	return func(h *CLIApprovalHandler) { h.timeout = d }
}

// WithYOLO enables auto-approve for all requests.
func WithYOLO(enabled bool) CLIOption {
	return func(h *CLIApprovalHandler) { h.yolo = enabled }
}

// NewCLIApprovalHandler creates a new interactive CLI approval handler.
func NewCLIApprovalHandler(opts ...CLIOption) *CLIApprovalHandler {
	h := &CLIApprovalHandler{
		reader:  os.Stdin,
		writer:  os.Stderr,
		timeout: 60 * time.Second,
	}
	for _, o := range opts {
		o(h)
	}
	return h
}

// AskApproval fulfills the permissions.AskApprovalFn signature.
func (h *CLIApprovalHandler) AskApproval(ctx context.Context, req permissions.ApprovalRequest) (permissions.ApprovalResponse, error) {
	if h.yolo {
		return permissions.ApprovalResponse{Allowed: true}, nil
	}

	// Print the formatted prompt.
	prompt := FormatApprovalPrompt(req)
	fmt.Fprint(h.writer, prompt)

	// Read user input with timeout.
	type result struct {
		input string
		err   error
	}
	ch := make(chan result, 1)
	go func() {
		scanner := bufio.NewScanner(h.reader)
		if scanner.Scan() {
			ch <- result{input: strings.TrimSpace(strings.ToLower(scanner.Text()))}
		} else {
			ch <- result{err: scanner.Err()}
		}
	}()

	var timeoutCh <-chan time.Time
	if h.timeout > 0 {
		timeoutCh = time.After(h.timeout)
	}

	select {
	case <-ctx.Done():
		return permissions.ApprovalResponse{}, ctx.Err()
	case <-timeoutCh:
		fmt.Fprintf(h.writer, "\n%s Timed out after %s — auto-denied.\n", colorRed("✗"), h.timeout)
		return permissions.ApprovalResponse{Allowed: false}, nil
	case r := <-ch:
		if r.err != nil {
			return permissions.ApprovalResponse{}, fmt.Errorf("reading approval input: %w", r.err)
		}
		return parseInput(r.input), nil
	}
}

// parseInput converts user input into an ApprovalResponse.
func parseInput(input string) permissions.ApprovalResponse {
	switch input {
	case "y", "yes":
		return permissions.ApprovalResponse{Allowed: true}
	case "n", "no", "":
		return permissions.ApprovalResponse{Allowed: false}
	case "a", "always":
		return permissions.ApprovalResponse{Allowed: true, Permanent: true}
	case "d", "deny":
		return permissions.ApprovalResponse{Allowed: false, Permanent: true}
	default:
		// Treat any unrecognized input as feedback + deny.
		return permissions.ApprovalResponse{Allowed: false, Feedback: input}
	}
}

// -------------------------------------------------------------------
// HeadlessApprovalHandler — policy-based auto-decisions
// -------------------------------------------------------------------

// HeadlessPolicy configures how the headless handler treats each risk level.
//
// Secure-by-default: the zero value denies Medium, High, and Critical.
// Operators MUST consciously opt in by setting the corresponding fields.
type HeadlessPolicy struct {
	AllowMedium bool // auto-approve medium risk (default: false → deny)
	AllowHigh   bool // auto-approve high risk   (default: false → deny)

	// AllowMediumRequiresConfirmation, when true, forces medium-risk
	// approvals to remain denied even if AllowMedium is also true.
	// It exists as a belt-and-braces flag so that operators who flip
	// AllowMedium in a config file still get an audited refusal until
	// they ALSO explicitly clear this flag — a two-key override for a
	// security-sensitive branch. A denial issued by this rule always
	// returns the feedback string
	// "MediumRisk auto-approval requires explicit override".
	AllowMediumRequiresConfirmation bool
}

// HeadlessApprovalHandler auto-decides based on risk level policy.
// Low risk is always approved; Critical is always denied.
type HeadlessApprovalHandler struct {
	policy HeadlessPolicy
}

// NewHeadlessApprovalHandler creates a headless handler with the given policy.
func NewHeadlessApprovalHandler(policy HeadlessPolicy) *HeadlessApprovalHandler {
	return &HeadlessApprovalHandler{policy: policy}
}

// AskApproval fulfills the permissions.AskApprovalFn signature.
//
// Every decision — approve or deny — is written to the process-wide
// audit logger BEFORE returning, so an attacker who smuggles a tool
// call past the classifier cannot execute silently.
func (h *HeadlessApprovalHandler) AskApproval(_ context.Context, req permissions.ApprovalRequest) (permissions.ApprovalResponse, error) {
	audit := CurrentAuditLogger()
	emit := func(resp permissions.ApprovalResponse, reason string) {
		audit.Log(AuditEntry{
			Timestamp: time.Now().UTC(),
			ToolName:  req.ToolName,
			RiskLevel: req.Risk.Level.String(),
			Decision:  decisionString(resp.Allowed),
			Reason:    reason,
			Input:     req.Args,
		})
	}

	switch req.Risk.Level {
	case permissions.ToolRiskSafe, permissions.ToolRiskLow:
		resp := permissions.ApprovalResponse{Allowed: true}
		emit(resp, "low/safe risk auto-approved in headless mode")
		return resp, nil

	case permissions.ToolRiskMedium:
		// Secure-by-default: deny unless the operator has *both*
		// opted in via AllowMedium AND explicitly cleared the
		// confirmation-required flag.
		if h.policy.AllowMedium && !h.policy.AllowMediumRequiresConfirmation {
			resp := permissions.ApprovalResponse{Allowed: true}
			emit(resp, "medium risk auto-approved via explicit policy override")
			return resp, nil
		}
		feedback := "medium risk denied in headless mode"
		if h.policy.AllowMedium && h.policy.AllowMediumRequiresConfirmation {
			feedback = "MediumRisk auto-approval requires explicit override"
		}
		resp := permissions.ApprovalResponse{Allowed: false, Feedback: feedback}
		emit(resp, feedback)
		return resp, nil

	case permissions.ToolRiskHigh:
		if h.policy.AllowHigh {
			resp := permissions.ApprovalResponse{Allowed: true}
			emit(resp, "high risk auto-approved via explicit policy override")
			return resp, nil
		}
		resp := permissions.ApprovalResponse{
			Allowed:  false,
			Feedback: "high risk denied in headless mode",
		}
		emit(resp, "high risk denied in headless mode")
		return resp, nil

	case permissions.ToolRiskCritical:
		resp := permissions.ApprovalResponse{
			Allowed:  false,
			Feedback: "critical risk auto-denied in headless mode",
		}
		emit(resp, "critical risk auto-denied in headless mode")
		return resp, nil

	default:
		resp := permissions.ApprovalResponse{Allowed: false}
		emit(resp, "unknown risk level auto-denied in headless mode")
		return resp, nil
	}
}

// -------------------------------------------------------------------
// FormatApprovalPrompt — pretty-prints a permission request
// -------------------------------------------------------------------

// FormatApprovalPrompt builds a formatted terminal prompt for an ApprovalRequest.
func FormatApprovalPrompt(req permissions.ApprovalRequest) string {
	var b strings.Builder

	riskLabel := strings.ToUpper(req.Risk.Level.String())
	riskColored := colorForRisk(req.Risk.Level, riskLabel)

	b.WriteString("\n")
	b.WriteString("┌─ Permission Required ──────────────────────\n")
	b.WriteString(fmt.Sprintf("│ Tool:  %s\n", req.ToolName))
	b.WriteString(fmt.Sprintf("│ Risk:  %s\n", riskColored))

	// Show command if present (bash tools).
	if cmd, ok := req.Args["command"].(string); ok && cmd != "" {
		// Truncate very long commands.
		if len(cmd) > 120 {
			cmd = cmd[:117] + "..."
		}
		b.WriteString(fmt.Sprintf("│ Cmd:   %s\n", cmd))
	}

	// Show file path if present.
	filePath := extractFilePath(req.Args)
	if filePath != "" {
		b.WriteString(fmt.Sprintf("│ File:  %s\n", filePath))
	}

	// Explanation from the permission explainer.
	if req.Explanation != nil {
		b.WriteString("│\n")
		if req.Explanation.Explanation != "" {
			b.WriteString(fmt.Sprintf("│ %s\n", req.Explanation.Explanation))
		}
		if req.Explanation.Risk != "" {
			b.WriteString(fmt.Sprintf("│ Risk: %s\n", req.Explanation.Risk))
		}
	} else if req.Reason != "" {
		b.WriteString("│\n")
		b.WriteString(fmt.Sprintf("│ %s\n", req.Reason))
	}

	b.WriteString("│\n")
	b.WriteString("│ [y]es  [n]o  [a]lways  [d]eny always\n")
	b.WriteString("└─────────────────────────────────────────────\n")
	b.WriteString("> ")

	return b.String()
}

// -------------------------------------------------------------------
// ANSI color helpers
// -------------------------------------------------------------------

func colorRed(s string) string    { return "\033[31m" + s + "\033[0m" }
func colorYellow(s string) string { return "\033[33m" + s + "\033[0m" }
func colorBlue(s string) string   { return "\033[34m" + s + "\033[0m" }
func colorGray(s string) string   { return "\033[90m" + s + "\033[0m" }

// colorForRisk returns the risk label colored according to severity.
func colorForRisk(level permissions.ToolRiskLevel, label string) string {
	switch level {
	case permissions.ToolRiskCritical:
		return colorRed("⚠ " + label)
	case permissions.ToolRiskHigh:
		return colorYellow(label)
	case permissions.ToolRiskMedium:
		return colorBlue(label)
	default:
		return colorGray(label)
	}
}

// extractFilePath gets the file path from tool arguments.
func extractFilePath(args map[string]any) string {
	for _, key := range []string{"file_path", "path", "filePath"} {
		if p, ok := args[key].(string); ok && p != "" {
			return p
		}
	}
	return ""
}

// -------------------------------------------------------------------
// Channel-relay constructor helpers
// -------------------------------------------------------------------

// NewTelegramHandler creates an ApprovalHandler backed by Telegram.
// Returns nil if BotToken or ChatID is empty.
func NewTelegramHandler(botToken, chatID string, timeout time.Duration) *TelegramApprovalRelay {
	if botToken == "" || chatID == "" {
		return nil
	}
	return NewTelegramApprovalRelay(TelegramRelayConfig{
		BotToken: botToken, ChatID: chatID, Timeout: timeout,
	})
}

// NewDiscordHandler creates an ApprovalHandler backed by Discord.
// Returns nil if BotToken or ChannelID is empty.
func NewDiscordHandler(botToken, channelID string, timeout time.Duration) *DiscordApprovalRelay {
	if botToken == "" || channelID == "" {
		return nil
	}
	return NewDiscordApprovalRelay(DiscordRelayConfig{
		BotToken: botToken, ChannelID: channelID, Timeout: timeout,
	})
}
