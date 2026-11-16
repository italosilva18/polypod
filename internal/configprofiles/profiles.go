// Package profiles — Config Profiles
package profiles

type ConfigProfile struct {
	Name     string
	Settings map[string]interface{}
}

type ProfileManager struct {
	profiles []ConfigProfile
	current  string
}

func NewProfileManager() *ProfileManager {
	return &ProfileManager{
		profiles: []ConfigProfile{
			{Name: "default", Settings: make(map[string]interface{})},
			{Name: "dev", Settings: make(map[string]interface{})},
			{Name: "prod", Settings: make(map[string]interface{})},
		},
	}
}

func (p *ProfileManager) CreateProfile(name string) {
	p.profiles = append(p.profiles, ConfigProfile{Name: name, Settings: make(map[string]interface{})})
}

func (p *ProfileManager) SwitchProfile(name string) {
	p.current = name
}

func (p *ProfileManager) GetCurrent() string {
	return p.current
}
