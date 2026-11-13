package commands

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/costa/polypod/internal/command"
)

func TestNativePRCommentsAggregatesPaginatedReviewData(t *testing.T) {
	bin := t.TempDir()
	script := `#!/bin/sh
case "$1" in
  pr)
    cat <<'JSON'
{"number":12,"title":"Corrigir parser","url":"https://github.com/acme/app/pull/12","reviewDecision":"CHANGES_REQUESTED"}
JSON
    ;;
  api)
    case "$4" in
      */issues/12/comments)
        cat <<'JSON'
[[{"body":"Veja o caso vazio","html_url":"https://github.com/acme/app/pull/12#issuecomment-1","user":{"login":"ana"}}],[]]
JSON
        ;;
      */pulls/12/reviews)
        cat <<'JSON'
[[{"body":"Ajustar testes","state":"CHANGES_REQUESTED","html_url":"https://github.com/acme/app/pull/12#pullrequestreview-1","user":{"login":"bia"}}]]
JSON
        ;;
      */pulls/12/comments)
    cat <<'JSON'
[[{"body":"Corrigir este ramo","path":"parser.go","line":27,"html_url":"https://github.com/acme/app/pull/12#discussion-1","user":{"login":"carol"}}],[]]
JSON
        ;;
      */pulls/12/files)
        cat <<'JSON'
[[{"filename":"parser.go","patch":"@@ -25,3 +25,3 @@\n old\n-old\n+new\n context"}]]
JSON
        ;;
      *) exit 3 ;;
    esac
    ;;
  *) exit 2 ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	reg := command.NewRegistry()
	RegisterNativePRCommands(reg)
	cmd, ok := reg.Get("pr-comments")
	if !ok {
		t.Fatal("native PR command missing")
	}
	result, err := cmd.Execute([]string{"12"}, command.CommandContext{WorkingDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"PR #12", "@ana", "@bia", "parser.go:27 [no diff atual]", "@carol"} {
		if !strings.Contains(result.Display, expected) {
			t.Fatalf("missing %q in report: %s", expected, result.Display)
		}
	}
}

func TestCommentInPatchUsesNewLineCoordinates(t *testing.T) {
	patch := "@@ -10,2 +10,3 @@\n old\n+one\n+two\n@@ -40 +41 @@\n-old\n+new\n"
	for _, line := range []int{10, 12, 41} {
		if !commentInPatch(patch, line) {
			t.Fatalf("new line %d missing", line)
		}
	}
	for _, line := range []int{9, 13, 40, 42} {
		if commentInPatch(patch, line) {
			t.Fatalf("line %d outside patch", line)
		}
	}
}

func TestNativePRCommentsRejectsInvalidIdentifier(t *testing.T) {
	for _, args := range [][]string{{"--repo"}, {"0"}, {"abc", "def"}} {
		if _, err := prRequestID(args); err == nil {
			t.Fatalf("accepted invalid request: %v", args)
		}
	}
	if _, _, err := parsePRRepo("https://github.com/acme/app/pull/99", 12); err == nil {
		t.Fatal("accepted mismatched PR URL")
	}
}

func TestNativePRCommentsHTTPFallbackPaginationAndAutofixContext(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"message":"Resource not accessible by personal access token"}`))
			return
		}
		switch {
		case r.URL.Path == "/repos/acme/goapp/pulls/55":
			_, _ = w.Write([]byte(`{"number":55,"title":"Tratar divisor zero","html_url":"https://github.com/acme/goapp/pull/55","state":"open"}`))
		case r.URL.Path == "/repos/acme/goapp/issues/55/comments":
			if r.URL.Query().Get("page") == "2" {
				_, _ = w.Write([]byte(`[{"body":"Comentário página 2","html_url":"https://github.com/acme/goapp/pull/55#issuecomment-2","user":{"login":"diego"}}]`))
				return
			}
			w.Header().Set("Link", fmt.Sprintf(`<%s/repos/acme/goapp/issues/55/comments?page=2>; rel="next"`, srv.URL))
			_, _ = w.Write([]byte(`[{"body":"Comentário página 1","html_url":"https://github.com/acme/goapp/pull/55#issuecomment-1","user":{"login":"ana"}}]`))
		case r.URL.Path == "/repos/acme/goapp/pulls/55/reviews":
			_, _ = w.Write([]byte(`[{"body":"Retornar erro em b == 0","state":"CHANGES_REQUESTED","html_url":"https://github.com/acme/goapp/pull/55#pullrequestreview-1","user":{"login":"bia"}}]`))
		case r.URL.Path == "/repos/acme/goapp/pulls/55/comments":
			_, _ = w.Write([]byte(`[{"body":"Falta checar b == 0 aqui","path":"math.go","line":14,"html_url":"https://github.com/acme/goapp/pull/55#discussion-1","user":{"login":"carol"}}]`))
		case r.URL.Path == "/repos/acme/goapp/pulls/55/files":
			_, _ = w.Write([]byte(`[{"filename":"math.go","patch":"@@ -12,4 +12,4 @@\n func Div(a, b int) int {\n-\treturn 0\n+\treturn a / b\n }"}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	oldOverride := prAPIBaseOverride
	prAPIBaseOverride = srv.URL
	t.Cleanup(func() { prAPIBaseOverride = oldOverride })

	// Force gh to fail so HTTP REST v3 fallback runs.
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte("#!/bin/sh\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("GITHUB_TOKEN", "github_pat_scoped_to_other_org")

	reg := command.NewRegistry()
	RegisterAutonomousCommands(reg)
	RegisterNativePRCommands(reg)

	prCmd, ok := reg.Get("pr_comments")
	if !ok {
		t.Fatal("pr_comments missing")
	}
	res, err := prCmd.Execute([]string{"https://github.com/acme/goapp/pull/55"}, command.CommandContext{WorkingDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"PR #55: Tratar divisor zero",
		"Decisão: CHANGES_REQUESTED",
		"Comentário página 1",
		"Comentário página 2",
		"Linha math.go:14 [no diff atual]",
		"Falta checar b == 0 aqui",
	} {
		if !strings.Contains(res.Display, want) {
			t.Fatalf("missing %q in HTTP fallback report:\n%s", want, res.Display)
		}
	}

	autofixCmd, ok := reg.Get("autofix-pr")
	if !ok {
		t.Fatal("autofix-pr missing")
	}
	prompt, err := autofixCmd.GetPrompt([]string{"https://github.com/acme/goapp/pull/55"}, command.CommandContext{WorkingDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "Contexto paginado do PR (/pr_comments):") || !strings.Contains(prompt, "Linha math.go:14 [no diff atual]") {
		t.Fatalf("autofix-pr prompt missing embedded PR report:\n%s", prompt)
	}
}
