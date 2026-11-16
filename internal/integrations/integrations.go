// Package integrations — Google + OAuth Integration
package integrations

type GoogleIntegration struct {
	driveEnabled bool
	docsEnabled  bool
	gmailEnabled bool
}

type OAuthFlow struct {
	provider     string
	clientID     string
	clientSecret string
}

func NewGoogleIntegration() *GoogleIntegration {
	return &GoogleIntegration{}
}

func NewOAuthFlow(provider, clientID, clientSecret string) *OAuthFlow {
	return &OAuthFlow{provider: provider, clientID: clientID, clientSecret: clientSecret}
}

func (g *GoogleIntegration) EnableAll() {
	g.driveEnabled = true
	g.docsEnabled = true
	g.gmailEnabled = true
}

func (o *OAuthFlow) StartAuth() string {
	return "auth_url"
}
