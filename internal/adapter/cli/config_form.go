package cli

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/costa/polypod/internal/config"
)

// ── Field types ─────────────────────────────────────────────────────────────

type configFieldType int

const (
	configFieldString configFieldType = iota
	configFieldInt
	configFieldBool
	configFieldChoice
	configFieldHidden // for API keys: shown as ***
	configFieldFloat
)

// configField describes one editable row in the config form.
type configField struct {
	Label   string
	Path    string // dotted path: "ai.model"
	Value   string
	Type    configFieldType
	Choices []string // for configFieldChoice
	// Min/Max are advisory hints rendered in the help line for numeric
	// fields. They are not strictly enforced.
	Min, Max float64
}

// configFormResult is returned from Update when the form needs the host
// to take action (close the overlay, persist, reload, etc.).
type configFormResult struct {
	Close  bool   // hide the form
	Saved  bool   // form was saved successfully
	Reload bool   // reload requested
	Notify string // optional status message for the host status bar
}

// configFormModel is a self-contained Bubbletea sub-model that renders
// an interactive overlay for editing the polypod config.
type configFormModel struct {
	fields    []configField
	selected  int
	editing   bool
	editValue string
	choiceIdx int // current index when editing a choice field

	width  int
	height int

	dirty   bool
	status  string // last action status (one-liner shown in the footer)
	cfgPath string // path used by Save / reload
	cfg     *config.Config
}

// newConfigForm builds a fresh form populated from the given config.
// cfgPath is the on-disk YAML path used by Save() and reloads.
func newConfigForm(cfg *config.Config, cfgPath string, agentChoices []string) *configFormModel {
	m := &configFormModel{
		cfg:     cfg,
		cfgPath: cfgPath,
	}
	m.rebuildFields(agentChoices)
	return m
}

// rebuildFields populates the visible field list from the underlying
// *config.Config. Called on construction and after a reload.
func (m *configFormModel) rebuildFields(agentChoices []string) {
	if m.cfg == nil {
		m.cfg = config.DefaultConfig()
	}

	providers := []string{"deepseek", "openai", "anthropic", "google", "groq"}

	// Default agent choices come from the registry; fall back to a
	// minimal common set if none were provided.
	if len(agentChoices) == 0 {
		agentChoices = []string{"coder", "default"}
	}
	defaultAgent := ""
	if m.cfg.Data.AgentsDir != "" {
		// We don't actually persist a "default agent" in the config
		// struct yet — pick the first registered one as a hint.
		defaultAgent = agentChoices[0]
	}

	autoCompact := false
	compactThreshold := 80
	streaming := true
	costLimit := 0.0

	m.fields = []configField{
		{
			Label:   "AI Provider",
			Path:    "ai.provider",
			Value:   m.cfg.AI.Provider,
			Type:    configFieldChoice,
			Choices: providers,
		},
		{
			Label: "AI Model",
			Path:  "ai.model",
			Value: m.cfg.AI.Model,
			Type:  configFieldString,
		},
		{
			Label: "API Key",
			Path:  "ai.api_key",
			Value: m.cfg.AI.APIKey,
			Type:  configFieldHidden,
		},
		{
			Label: "Max Tokens",
			Path:  "ai.max_tokens",
			Value: strconv.Itoa(m.cfg.AI.MaxToks),
			Type:  configFieldInt,
			Min:   1,
			Max:   200000,
		},
		{
			Label: "Temperature",
			Path:  "ai.temperature",
			Value: strconv.FormatFloat(float64(m.cfg.AI.Temp), 'f', 2, 32),
			Type:  configFieldFloat,
			Min:   0,
			Max:   1,
		},
		{
			Label: "Data Dir",
			Path:  "data.dir",
			Value: m.cfg.Data.Dir,
			Type:  configFieldString,
		},
		{
			Label: "Auto Compact",
			Path:  "ai.auto_compact",
			Value: fmt.Sprintf("%v", autoCompact),
			Type:  configFieldBool,
		},
		{
			Label: "Compact Threshold",
			Path:  "ai.compact_threshold",
			Value: strconv.Itoa(compactThreshold),
			Type:  configFieldInt,
			Min:   50,
			Max:   95,
		},
		{
			Label:   "Default Agent",
			Path:    "agents.default",
			Value:   defaultAgent,
			Type:    configFieldChoice,
			Choices: agentChoices,
		},
		{
			Label: "Streaming",
			Path:  "ai.streaming",
			Value: fmt.Sprintf("%v", streaming),
			Type:  configFieldBool,
		},
		{
			Label: "Cost Limit",
			Path:  "ai.cost_limit",
			Value: strconv.FormatFloat(costLimit, 'f', 2, 64),
			Type:  configFieldFloat,
			Min:   0,
			Max:   1000,
		},
	}
}

// SetSize is called by the host model on window resize.
func (m *configFormModel) SetSize(w, h int) {
	m.width = w
	m.height = h
}

// Dirty reports whether the form has unsaved changes.
func (m *configFormModel) Dirty() bool { return m.dirty }

// Update handles a Bubbletea message and returns the (possibly mutated)
// form, an optional command, and a result describing the action the host
// should take.
func (m *configFormModel) Update(msg tea.Msg) (*configFormModel, tea.Cmd, configFormResult) {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil, configFormResult{}
	}

	if m.editing {
		return m.updateEditing(keyMsg)
	}
	return m.updateNavigating(keyMsg)
}

func (m *configFormModel) updateNavigating(msg tea.KeyMsg) (*configFormModel, tea.Cmd, configFormResult) {
	switch msg.Type {
	case tea.KeyUp:
		if m.selected > 0 {
			m.selected--
		}
		return m, nil, configFormResult{}
	case tea.KeyDown:
		if m.selected < len(m.fields)-1 {
			m.selected++
		}
		return m, nil, configFormResult{}
	case tea.KeyEsc:
		// Esc closes the form (without saving). Caller decides
		// whether to warn about unsaved changes.
		return m, nil, configFormResult{Close: true}
	case tea.KeyEnter:
		return m.beginEdit()
	case tea.KeyLeft:
		// Quick toggle for booleans / cycle backward for choices.
		return m.quickAdjust(-1)
	case tea.KeyRight:
		return m.quickAdjust(+1)
	}

	switch strings.ToLower(msg.String()) {
	case "s":
		if err := m.Save(); err != nil {
			m.status = "erro ao salvar: " + err.Error()
			return m, nil, configFormResult{Notify: m.status}
		}
		m.status = "salvo em " + m.cfgPath
		return m, nil, configFormResult{Saved: true, Notify: m.status}
	case "r":
		if err := m.Reload(); err != nil {
			m.status = "erro ao recarregar: " + err.Error()
			return m, nil, configFormResult{Notify: m.status}
		}
		m.status = "recarregado de " + m.cfgPath
		return m, nil, configFormResult{Reload: true, Notify: m.status}
	case "q":
		return m, nil, configFormResult{Close: true}
	}
	return m, nil, configFormResult{}
}

func (m *configFormModel) updateEditing(msg tea.KeyMsg) (*configFormModel, tea.Cmd, configFormResult) {
	field := &m.fields[m.selected]

	switch field.Type {
	case configFieldChoice:
		switch msg.Type {
		case tea.KeyLeft, tea.KeyUp:
			if m.choiceIdx > 0 {
				m.choiceIdx--
			} else {
				m.choiceIdx = len(field.Choices) - 1
			}
			return m, nil, configFormResult{}
		case tea.KeyRight, tea.KeyDown:
			m.choiceIdx = (m.choiceIdx + 1) % len(field.Choices)
			return m, nil, configFormResult{}
		case tea.KeyEnter:
			field.Value = field.Choices[m.choiceIdx]
			m.dirty = true
			m.editing = false
			return m, nil, configFormResult{}
		case tea.KeyEsc:
			m.editing = false
			return m, nil, configFormResult{}
		}
		return m, nil, configFormResult{}

	case configFieldBool:
		switch msg.Type {
		case tea.KeyEsc:
			m.editing = false
			return m, nil, configFormResult{}
		case tea.KeyEnter, tea.KeySpace:
			if strings.EqualFold(field.Value, "true") {
				field.Value = "false"
			} else {
				field.Value = "true"
			}
			m.dirty = true
			m.editing = false
			return m, nil, configFormResult{}
		}
		return m, nil, configFormResult{}

	default:
		// Text-style input: edit a buffer.
		switch msg.Type {
		case tea.KeyEsc:
			m.editing = false
			m.editValue = ""
			return m, nil, configFormResult{}
		case tea.KeyEnter:
			// Commit value with light validation.
			if err := m.validateEditValue(field); err != nil {
				m.status = err.Error()
				return m, nil, configFormResult{}
			}
			field.Value = m.editValue
			m.dirty = true
			m.editing = false
			m.editValue = ""
			return m, nil, configFormResult{}
		case tea.KeyBackspace:
			if len(m.editValue) > 0 {
				// Trim by one rune (UTF-8 safe).
				r := []rune(m.editValue)
				m.editValue = string(r[:len(r)-1])
			}
			return m, nil, configFormResult{}
		case tea.KeyRunes, tea.KeySpace:
			m.editValue += string(msg.Runes)
			return m, nil, configFormResult{}
		}
	}
	return m, nil, configFormResult{}
}

func (m *configFormModel) beginEdit() (*configFormModel, tea.Cmd, configFormResult) {
	field := &m.fields[m.selected]
	switch field.Type {
	case configFieldChoice:
		m.editing = true
		m.choiceIdx = 0
		for i, c := range field.Choices {
			if c == field.Value {
				m.choiceIdx = i
				break
			}
		}
	case configFieldBool:
		// Toggle in place; no edit buffer needed.
		if strings.EqualFold(field.Value, "true") {
			field.Value = "false"
		} else {
			field.Value = "true"
		}
		m.dirty = true
	default:
		m.editing = true
		m.editValue = field.Value
	}
	return m, nil, configFormResult{}
}

func (m *configFormModel) quickAdjust(delta int) (*configFormModel, tea.Cmd, configFormResult) {
	field := &m.fields[m.selected]
	switch field.Type {
	case configFieldBool:
		if strings.EqualFold(field.Value, "true") {
			field.Value = "false"
		} else {
			field.Value = "true"
		}
		m.dirty = true
	case configFieldChoice:
		idx := 0
		for i, c := range field.Choices {
			if c == field.Value {
				idx = i
				break
			}
		}
		idx += delta
		if idx < 0 {
			idx = len(field.Choices) - 1
		}
		if idx >= len(field.Choices) {
			idx = 0
		}
		field.Value = field.Choices[idx]
		m.dirty = true
	}
	return m, nil, configFormResult{}
}

func (m *configFormModel) validateEditValue(field *configField) error {
	v := strings.TrimSpace(m.editValue)
	switch field.Type {
	case configFieldInt:
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("valor invalido: precisa ser inteiro")
		}
		if field.Max > 0 && (float64(n) < field.Min || float64(n) > field.Max) {
			return fmt.Errorf("valor fora de [%g, %g]", field.Min, field.Max)
		}
	case configFieldFloat:
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return fmt.Errorf("valor invalido: precisa ser numero")
		}
		if field.Max > 0 && (f < field.Min || f > field.Max) {
			return fmt.Errorf("valor fora de [%g, %g]", field.Min, field.Max)
		}
	}
	return nil
}

// Apply writes the form's current field values back to the given config
// struct. Only fields that map to known struct paths are persisted.
func (m *configFormModel) Apply(cfg *config.Config) error {
	if cfg == nil {
		return fmt.Errorf("config is nil")
	}
	for _, f := range m.fields {
		if err := setConfigByPath(cfg, f.Path, f.Value, f.Type); err != nil {
			return fmt.Errorf("%s: %w", f.Path, err)
		}
	}
	return nil
}

// Save persists the form values to disk by applying them to the
// underlying config struct, then writing it as YAML.
func (m *configFormModel) Save() error {
	if m.cfg == nil {
		m.cfg = config.DefaultConfig()
	}
	if err := m.Apply(m.cfg); err != nil {
		return err
	}
	if m.cfgPath == "" {
		return fmt.Errorf("nenhum caminho de config configurado")
	}
	data, err := m.cfg.Marshal()
	if err != nil {
		return err
	}
	if err := os.WriteFile(m.cfgPath, data, 0644); err != nil {
		return err
	}
	m.dirty = false
	return nil
}

// Reload re-reads the config from disk and rebuilds the field list.
func (m *configFormModel) Reload() error {
	if m.cfgPath == "" {
		return fmt.Errorf("nenhum caminho de config configurado")
	}
	cfg, err := config.Load(m.cfgPath)
	if err != nil {
		return err
	}
	m.cfg = cfg
	m.dirty = false
	// Preserve agent choices if they were set previously.
	var agents []string
	for _, f := range m.fields {
		if f.Path == "agents.default" {
			agents = f.Choices
			break
		}
	}
	m.rebuildFields(agents)
	return nil
}

// setConfigByPath updates a single dotted-path field on the config
// struct. Unknown paths are ignored so the form can carry "virtual"
// fields (like ai.streaming) that have no current YAML home.
func setConfigByPath(cfg *config.Config, path, value string, typ configFieldType) error {
	switch path {
	case "ai.provider":
		cfg.AI.Provider = value
	case "ai.model":
		cfg.AI.Model = value
	case "ai.api_key":
		// Don't overwrite the value with the masked placeholder if the
		// user did not actively edit it.
		if value != "" && !strings.HasPrefix(value, "***") {
			cfg.AI.APIKey = value
		}
	case "ai.max_tokens":
		n, err := strconv.Atoi(value)
		if err != nil {
			return err
		}
		cfg.AI.MaxToks = n
	case "ai.temperature":
		f, err := strconv.ParseFloat(value, 32)
		if err != nil {
			return err
		}
		cfg.AI.Temp = float32(f)
	case "data.dir":
		cfg.Data.Dir = value
	case "ai.auto_compact", "ai.compact_threshold",
		"agents.default", "ai.streaming", "ai.cost_limit":
		// Virtual fields with no YAML home in the current Config
		// struct. Saving them is a no-op for now; the form still
		// renders the value so the UX is consistent and a future
		// schema bump can pick them up.
		_ = value
	default:
		// Unknown path: ignore.
	}
	_ = typ
	return nil
}

// ── View ────────────────────────────────────────────────────────────────────

func (m *configFormModel) View() string {
	if m.cfg == nil {
		return ""
	}

	var b strings.Builder

	// Header
	title := configFormTitleStyle.Render("⚙ POLYPOD CONFIG")
	right := ""
	if m.dirty {
		right = configFormDirtyStyle.Render(" [unsaved]")
	}
	b.WriteString(title)
	b.WriteString(right)
	b.WriteString("\n")
	b.WriteString(separatorStyle.Render(strings.Repeat("─", configFormBoxWidth-4)))
	b.WriteString("\n\n")

	// Compute label width.
	maxLabel := 0
	for _, f := range m.fields {
		if len(f.Label) > maxLabel {
			maxLabel = len(f.Label)
		}
	}

	for i, f := range m.fields {
		isSelected := i == m.selected
		isEditing := isSelected && m.editing

		var line strings.Builder

		// Selection arrow
		if isSelected {
			line.WriteString(configFormArrowStyle.Render("► "))
		} else {
			line.WriteString("  ")
		}

		// Label
		labelPad := strings.Repeat(" ", maxLabel-len(f.Label))
		line.WriteString(configFormLabelStyle.Render(f.Label + labelPad))
		line.WriteString("  ")

		// Value renderer
		line.WriteString(renderConfigValue(&f, isEditing, m.editValue, m.choiceIdx))

		b.WriteString(line.String())
		b.WriteString("\n")
	}

	// Footer
	b.WriteString("\n")
	if m.status != "" {
		b.WriteString(configFormStatusStyle.Render("  " + m.status))
		b.WriteString("\n")
	}
	hint := "↑↓ navegar  Enter editar  ←→ ajustar  s salvar  r recarregar  Esc/q sair"
	if m.editing {
		switch m.fields[m.selected].Type {
		case configFieldChoice:
			hint = "←→ alternar  Enter confirmar  Esc cancelar"
		case configFieldBool:
			hint = "Espaco/Enter alternar  Esc cancelar"
		default:
			hint = "Digite valor  Enter confirmar  Esc cancelar"
		}
	}
	b.WriteString(configFormHintStyle.Render("  " + hint))

	return configFormBoxStyle.Render(b.String())
}

// renderConfigValue formats a single field's value for display.
func renderConfigValue(f *configField, editing bool, buf string, choiceIdx int) string {
	switch f.Type {
	case configFieldHidden:
		if f.Value == "" {
			return configFormEmptyStyle.Render("(vazio)")
		}
		// Show prefix + asterisks
		mask := "***"
		prefix := ""
		if len(f.Value) > 3 {
			prefix = f.Value[:3]
		}
		return configFormValueStyle.Render(prefix + mask + "***")

	case configFieldBool:
		on := strings.EqualFold(f.Value, "true")
		if on {
			return configFormBoolOnStyle.Render("[✓] enabled")
		}
		return configFormBoolOffStyle.Render("[ ] disabled")

	case configFieldChoice:
		if editing {
			// Show options inline with current selection highlighted.
			var b strings.Builder
			b.WriteString(configFormValueStyle.Render("["))
			for i, c := range f.Choices {
				if i > 0 {
					b.WriteString(configFormLabelStyle.Render(" | "))
				}
				if i == choiceIdx {
					b.WriteString(configFormChoiceActiveStyle.Render(c))
				} else {
					b.WriteString(configFormLabelStyle.Render(c))
				}
			}
			b.WriteString(configFormValueStyle.Render("]"))
			return b.String()
		}
		val := f.Value
		if val == "" {
			val = "(unset)"
		}
		return configFormValueStyle.Render("[" + val + " ▼]")

	default:
		if editing {
			return configFormEditStyle.Render(buf + "▏")
		}
		val := f.Value
		if val == "" {
			val = configFormEmptyStyle.Render("(vazio)")
			return val
		}
		return configFormValueStyle.Render(val)
	}
}

// ── Overlay placement ──────────────────────────────────────────────────────

const configFormBoxWidth = 60

// Overlay returns the form rendered centered on a screen of the given size.
func (m *configFormModel) Overlay(width, height int) string {
	body := m.View()
	if width <= 0 || height <= 0 {
		return body
	}
	return lipgloss.Place(
		width, height,
		lipgloss.Center, lipgloss.Center,
		body,
	)
}

// ── Styles ──────────────────────────────────────────────────────────────────

var (
	configFormBoxStyle = lipgloss.NewStyle().
				BorderStyle(lipgloss.DoubleBorder()).
				BorderForeground(colorCyan).
				Padding(1, 2).
				Width(configFormBoxWidth)

	configFormTitleStyle = lipgloss.NewStyle().
				Foreground(colorCyan).
				Bold(true)

	configFormDirtyStyle = lipgloss.NewStyle().
				Foreground(colorYellow).
				Bold(true)

	configFormLabelStyle = lipgloss.NewStyle().
				Foreground(colorWhiteDim)

	configFormValueStyle = lipgloss.NewStyle().
				Foreground(colorWhite)

	configFormArrowStyle = lipgloss.NewStyle().
				Foreground(colorCyan).
				Bold(true)

	configFormBoolOnStyle = lipgloss.NewStyle().
				Foreground(colorGreen)

	configFormBoolOffStyle = lipgloss.NewStyle().
				Foreground(colorDim)

	configFormChoiceActiveStyle = lipgloss.NewStyle().
					Foreground(colorGreen).
					Bold(true)

	configFormEmptyStyle = lipgloss.NewStyle().
				Foreground(colorDim).
				Italic(true)

	configFormEditStyle = lipgloss.NewStyle().
				Foreground(colorYellow).
				Bold(true)

	configFormStatusStyle = lipgloss.NewStyle().
				Foreground(colorYellow)

	configFormHintStyle = lipgloss.NewStyle().
				Foreground(colorSubtle)
)

// configFieldByPath finds a field by its dotted path (used by /config get/set).
func (m *configFormModel) configFieldByPath(path string) *configField {
	for i := range m.fields {
		if m.fields[i].Path == path {
			return &m.fields[i]
		}
	}
	return nil
}
