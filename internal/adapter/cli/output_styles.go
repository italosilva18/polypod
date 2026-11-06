package cli

import (
	"fmt"
	"sort"
	"sync"

	"github.com/charmbracelet/lipgloss"
)

// OutputStyle controls how the assistant's responses are formatted both
// in terms of generated content (via injected system-prompt instructions)
// and visual rendering in the TUI.
type OutputStyle struct {
	// Identity
	Name        string
	Description string

	// Instructions are appended to the system prompt the next time the
	// model is invoked. They should describe the desired voice/format
	// in plain language.
	Instructions string

	// Visual rendering hints. The TUI is free to honour or ignore these.
	Compact    bool // less whitespace between blocks
	NoEmoji    bool // strip emoji from rendered output
	NoMarkdown bool // render as plain text only
	WrapWidth  int  // 0 = use terminal width

	// Style overrides applied when rendering the corresponding markdown
	// element. Zero-value styles fall through to the active theme.
	HeadingStyle lipgloss.Style
	CodeStyle    lipgloss.Style
	QuoteStyle   lipgloss.Style
}

// OutputStyleRegistry stores all known output styles and tracks which
// one is currently active. It is safe for concurrent use.
type OutputStyleRegistry struct {
	mu      sync.RWMutex
	styles  map[string]*OutputStyle
	order   []string
	current string
}

// NewOutputStyleRegistry returns a registry seeded with the built-in
// styles and "default" as the active style.
func NewOutputStyleRegistry() *OutputStyleRegistry {
	r := &OutputStyleRegistry{
		styles: make(map[string]*OutputStyle),
	}
	for _, s := range builtinOutputStyles() {
		r.Register(s)
	}
	r.current = "default"
	return r
}

// Register adds or replaces a style. Registering a nil style or a style
// with an empty Name is a no-op.
func (r *OutputStyleRegistry) Register(s *OutputStyle) {
	if s == nil || s.Name == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.styles[s.Name]; !exists {
		r.order = append(r.order, s.Name)
	}
	r.styles[s.Name] = s
}

// Get returns the named style or nil when no such style is registered.
func (r *OutputStyleRegistry) Get(name string) *OutputStyle {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.styles[name]
}

// Current returns the currently active style. It is guaranteed to be
// non-nil for a registry created with NewOutputStyleRegistry.
func (r *OutputStyleRegistry) Current() *OutputStyle {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if s, ok := r.styles[r.current]; ok {
		return s
	}
	if s, ok := r.styles["default"]; ok {
		return s
	}
	return nil
}

// Set switches the active style. Returns an error if the requested style
// is not registered.
func (r *OutputStyleRegistry) Set(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.styles[name]; !ok {
		return fmt.Errorf("output style %q not found", name)
	}
	r.current = name
	return nil
}

// CurrentName returns the active style's name without dereferencing.
func (r *OutputStyleRegistry) CurrentName() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.current
}

// List returns every registered style sorted alphabetically by Name.
func (r *OutputStyleRegistry) List() []*OutputStyle {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*OutputStyle, 0, len(r.styles))
	for _, s := range r.styles {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ---------------------------------------------------------------------------
// Built-in styles
// ---------------------------------------------------------------------------

func builtinOutputStyles() []*OutputStyle {
	heading := lipgloss.NewStyle().Bold(true).Foreground(colorCyan)
	code := lipgloss.NewStyle().Foreground(colorGreen)
	quote := lipgloss.NewStyle().Foreground(colorWhiteDim).Italic(true)

	return []*OutputStyle{
		{
			Name:         "default",
			Description:  "Renderizacao padrao em markdown com emojis",
			Instructions: "",
			HeadingStyle: heading,
			CodeStyle:    code,
			QuoteStyle:   quote,
		},
		{
			Name:        "compact",
			Description: "Menos espacamento, respostas curtas",
			Instructions: "Seja conciso. Respostas devem caber em poucas linhas. " +
				"Evite paragrafos longos e listas verbosas.",
			Compact:      true,
			HeadingStyle: heading,
			CodeStyle:    code,
			QuoteStyle:   quote,
		},
		{
			Name:        "plain",
			Description: "Texto puro, sem markdown nem emojis",
			Instructions: "Responda em texto puro. Nao use markdown, " +
				"emojis ou formatacao especial. Use apenas paragrafos simples.",
			NoEmoji:    true,
			NoMarkdown: true,
		},
		{
			Name:        "detailed",
			Description: "Respostas longas com explicacoes completas",
			Instructions: "Seja detalhado. Inclua contexto, justificativa e " +
				"alternativas relevantes em cada resposta.",
			HeadingStyle: heading,
			CodeStyle:    code,
			QuoteStyle:   quote,
		},
		{
			Name:        "explanatory",
			Description: "Sempre explica o raciocinio antes de agir",
			Instructions: "Antes de qualquer acao, explique o raciocinio em " +
				"uma ou duas frases. Depois execute. Apos executar, resuma o " +
				"que foi feito.",
			HeadingStyle: heading,
			CodeStyle:    code,
			QuoteStyle:   quote,
		},
		{
			Name:        "terse",
			Description: "Minimo possivel, codigo sempre que der",
			Instructions: "Minimize palavras. Prefira codigo a explicacoes. " +
				"Quando uma explicacao for indispensavel, use no maximo uma frase.",
			Compact:    true,
			NoEmoji:    true,
			NoMarkdown: false,
			CodeStyle:  code,
		},
		{
			Name:        "mentor",
			Description: "Tom educacional, explica conceitos por tras",
			Instructions: "Aja como um mentor experiente. Para cada solucao, " +
				"explique o porque, mencione armadilhas comuns e indique como " +
				"o leitor pode aprender mais.",
			HeadingStyle: heading,
			CodeStyle:    code,
			QuoteStyle:   quote,
		},
		{
			Name:        "expert",
			Description: "Assume usuario expert, sem mao na roda",
			Instructions: "Assuma um leitor experiente. Pule explicacoes basicas, " +
				"omita disclaimers e va direto a solucao tecnica.",
			Compact:      true,
			HeadingStyle: heading,
			CodeStyle:    code,
			QuoteStyle:   quote,
		},
	}
}
