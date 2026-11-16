// Package themepicker — Theme Picker with Preview
package themepicker

type Theme struct {
	Name        string
	Primary     string
	Background  string
	Foreground  string
	Preview     string
}

type ThemePicker struct {
	themes  []Theme
	current string
}

func NewThemePicker() *ThemePicker {
	return &ThemePicker{
		themes: []Theme{
			{Name: "Default", Primary: "#007ACC", Background: "#1E1E1E", Foreground: "#D4D4D4"},
			{Name: "Dark", Primary: "#007ACC", Background: "#000000", Foreground: "#FFFFFF"},
			{Name: "Light", Primary: "#007ACC", Background: "#FFFFFF", Foreground: "#000000"},
			{Name: "Monokai", Primary: "#A6E22E", Background: "#272822", Foreground: "#F8F8F2"},
		},
	}
}

func (t *ThemePicker) GetThemes() []Theme {
	return t.themes
}

func (t *ThemePicker) SetTheme(name string) {
	t.current = name
}

func (t *ThemePicker) GetCurrent() string {
	return t.current
}
