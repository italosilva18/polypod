package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectNodeScriptsUsesDeclaredScripts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "package.json")
	for _, tc := range []struct {
		name, manifest                string
		wantTest, wantLint, wantBuild string
	}{
		{"real scripts", `{"scripts":{"test":"vitest","lint":"eslint .","build":"vite build"}}`, "npm test", "npm run lint", "npm run build"},
		{"placeholder", `{"scripts":{"test":"echo \"Error: no test specified\" && exit 1"}}`, "", "", ""},
		{"unrelated key", `{"description":"test","dependencies":{"lint":"1"}}`, "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(tc.manifest), 0o600); err != nil {
				t.Fatal(err)
			}
			test, lint, build := detectNodeScripts(path)
			if test != tc.wantTest || lint != tc.wantLint || build != tc.wantBuild {
				t.Fatalf("commands = %q, %q, %q", test, lint, build)
			}
		})
	}
}
