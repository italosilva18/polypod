package doctor

import (
	"github.com/costa/polypod/internal/config"
	"testing"
)

func TestRemoteAuthorizationDiagnostic(t *testing.T) {
	tests := []struct {
		name, chat string
		users      []int64
		want       string
	}{
		{name: "private", chat: "42", want: "ok"},
		{name: "group unrestricted", chat: "-42", want: "fail"},
		{name: "group allowlist", chat: "-42", users: []int64{7}, want: "ok"},
		{name: "invalid user", chat: "42", users: []int64{0}, want: "fail"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := checkRemote(config.RemoteConfig{Enabled: true, BotToken: "secret", ChatID: tt.chat, AllowedUserIDs: tt.users})
			if got.Status != tt.want {
				t.Fatalf("%+v", got)
			}
		})
	}
}
