// Package secrets — Secrets Management
package secrets

type SecretManager struct {
	secrets map[string]string
}

func NewSecretManager() *SecretManager {
	return &SecretManager{
		secrets: make(map[string]string),
	}
}

func (s *SecretManager) Set(key, value string) {
	s.secrets[key] = value
}

func (s *SecretManager) Get(key string) (string, bool) {
	value, ok := s.secrets[key]
	return value, ok
}

func (s *SecretManager) Delete(key string) {
	delete(s.secrets, key)
}

func (s *SecretManager) List() []string {
	keys := make([]string, 0, len(s.secrets))
	for k := range s.secrets {
		keys = append(keys, k)
	}
	return keys
}
