package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/costa/polypod/internal/command"
)

const (
	maxPRJSON      = 4 << 20
	maxPRReport    = 48 << 10
	maxPRItems     = 200
	maxPRBodyRunes = 1500
	maxHTTPPages   = 10
)

type prIdentity struct {
	Number         int    `json:"number"`
	Title          string `json:"title"`
	URL            string `json:"url"`
	HTMLURL        string `json:"html_url"`
	State          string `json:"state"`
	ReviewDecision string `json:"reviewDecision"`
}

type prIssueComment struct {
	Body string `json:"body"`
	URL  string `json:"html_url"`
	User struct {
		Login string `json:"login"`
	} `json:"user"`
}

type prReview struct {
	Body  string `json:"body"`
	State string `json:"state"`
	URL   string `json:"html_url"`
	User  struct {
		Login string `json:"login"`
	} `json:"user"`
}

type prLineComment struct {
	Body    string `json:"body"`
	Path    string `json:"path"`
	Line    int    `json:"line"`
	OldLine int    `json:"original_line"`
	URL     string `json:"html_url"`
	User    struct {
		Login string `json:"login"`
	} `json:"user"`
}

type prFile struct {
	Filename string `json:"filename"`
	Patch    string `json:"patch"`
}

var (
	prHunkHeader  = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)
	linkNextRegex = regexp.MustCompile(`<([^>]+)>;\s*rel="next"`)
)

// prAPIBaseOverride is used only by tests to point HTTP fallback at httptest.Server.
var prAPIBaseOverride string

func commentInPatch(patch string, line int) bool {
	if line <= 0 {
		return false
	}
	for _, row := range strings.Split(patch, "\n") {
		groups := prHunkHeader.FindStringSubmatch(row)
		if len(groups) == 0 {
			continue
		}
		start, _ := strconv.Atoi(groups[3])
		count := 1
		if groups[4] != "" {
			count, _ = strconv.Atoi(groups[4])
		}
		if line >= start && line < start+count {
			return true
		}
	}
	return false
}

type cappedPRBuffer struct{ bytes.Buffer }

func (b *cappedPRBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > maxPRJSON {
		return 0, fmt.Errorf("resposta do GitHub excede 4 MiB")
	}
	return b.Buffer.Write(p)
}

func runGHRead(wd string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", args...)
	cmd.Dir = wd
	var output cappedPRBuffer
	cmd.Stdout = &output
	var stderr cappedPRBuffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("GitHub demorou demais: %w", ctx.Err())
		}
		return nil, fmt.Errorf("gh %s falhou: %w", args[0], err)
	}
	return output.Bytes(), nil
}

func parsePRRepo(raw string, number int) (string, string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		return "", "", fmt.Errorf("URL do PR inválida")
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 4 || parts[2] != "pull" || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("URL do PR não contém repositório e número esperados")
	}
	if number > 0 && parts[3] != strconv.Itoa(number) {
		return "", "", fmt.Errorf("URL do PR não contém repositório e número esperados")
	}
	return u.Hostname(), parts[0] + "/" + parts[1], nil
}

func parsePRURL(raw string) (host, repo string, number int, err error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		return "", "", 0, fmt.Errorf("URL do PR inválida")
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 4 || parts[2] != "pull" || parts[0] == "" || parts[1] == "" {
		return "", "", 0, fmt.Errorf("URL do PR inválida")
	}
	n, convErr := strconv.Atoi(parts[3])
	if convErr != nil || n <= 0 {
		return "", "", 0, fmt.Errorf("número do PR inválido na URL")
	}
	return u.Hostname(), parts[0] + "/" + parts[1], n, nil
}

func resolveGitHubRepoFromOrigin(wd string) (string, string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "remote", "get-url", "origin")
	cmd.Dir = wd
	out, err := cmd.Output()
	if err != nil {
		return "", "", false
	}
	remote := strings.TrimSpace(string(out))
	if after, ok := strings.CutPrefix(remote, "git@github.com:"); ok {
		repo := strings.TrimSuffix(after, ".git")
		if strings.Count(repo, "/") == 1 {
			return "github.com", repo, true
		}
	}
	u, err := url.Parse(remote)
	if err != nil || u.Hostname() == "" {
		return "", "", false
	}
	repo := strings.Trim(strings.TrimSuffix(u.Path, ".git"), "/")
	if strings.Count(repo, "/") != 1 {
		return "", "", false
	}
	return u.Hostname(), repo, true
}

func prRequestID(args []string) (string, error) {
	if len(args) == 0 {
		return "", nil
	}
	if len(args) != 1 {
		return "", fmt.Errorf("uso: /pr_comments [número ou URL]")
	}
	if n, err := strconv.Atoi(args[0]); err == nil && n > 0 {
		return strconv.Itoa(n), nil
	}
	u, err := url.Parse(args[0])
	if err == nil && u.Scheme == "https" && u.Hostname() != "" && strings.Contains(u.Path, "/pull/") {
		return args[0], nil
	}
	return "", fmt.Errorf("uso: /pr_comments [número ou URL]")
}

// FetchPRReport reads PR identity, general comments, reviews, inline diff
// comments, and file patches with pagination (via gh CLI first, falling back to
// GitHub's official REST API v3 over HTTPS when gh is unavailable or unauthenticated).
func FetchPRReport(wd string, args []string) (string, error) {
	id, err := prRequestID(args)
	if err != nil {
		return "", err
	}
	viewArgs := []string{"pr", "view"}
	if id != "" {
		viewArgs = append(viewArgs, id)
	}
	viewArgs = append(viewArgs, "--json", "number,title,url,reviewDecision")
	viewData, ghErr := runGHRead(wd, viewArgs...)
	if ghErr == nil {
		var pr prIdentity
		if err := json.Unmarshal(viewData, &pr); err != nil {
			return "", fmt.Errorf("Resposta de PR inválida: %w", err)
		}
		host, repo, err := parsePRRepo(pr.URL, pr.Number)
		if err != nil {
			return "", err
		}
		report, apiErr := fetchPRPagesViaGH(wd, host, repo, pr)
		if apiErr == nil {
			return report, nil
		}
		ghErr = apiErr
	}

	// Fallback to GitHub REST API v3 over HTTPS when gh is missing or unauthenticated.
	report, httpErr := fetchPRReportViaHTTP(wd, id)
	if httpErr == nil {
		return report, nil
	}
	return "", fmt.Errorf("%w (fallback HTTP: %v)", ghErr, httpErr)
}

func fetchPRPagesViaGH(wd, host, repo string, pr prIdentity) (string, error) {
	base := fmt.Sprintf("repos/%s", repo)
	var general [][]prIssueComment
	var reviews [][]prReview
	var lines [][]prLineComment
	var files [][]prFile
	queries := []struct {
		label, endpoint string
		target          any
	}{
		{"Comentários gerais", fmt.Sprintf("%s/issues/%d/comments", base, pr.Number), &general},
		{"Revisões", fmt.Sprintf("%s/pulls/%d/reviews", base, pr.Number), &reviews},
		{"Comentários por linha", fmt.Sprintf("%s/pulls/%d/comments", base, pr.Number), &lines},
		{"Arquivos do diff", fmt.Sprintf("%s/pulls/%d/files", base, pr.Number), &files},
	}
	errorsByQuery := make([]error, len(queries))
	var wg sync.WaitGroup
	for i, query := range queries {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errorsByQuery[i] = readPRPages(wd, host, query.endpoint, query.target)
		}()
	}
	wg.Wait()
	for i, query := range queries {
		if errorsByQuery[i] != nil {
			if i == len(queries)-1 {
				continue // review comments remain useful if patch previews are unavailable
			}
			return "", fmt.Errorf("%s: %w", query.label, errorsByQuery[i])
		}
	}
	return formatPRReport(pr, general, reviews, lines, files), nil
}

func githubAPIBase(host string) (string, error) {
	if prAPIBaseOverride != "" {
		return strings.TrimRight(prAPIBaseOverride, "/"), nil
	}
	if strings.EqualFold(host, "github.com") {
		return "https://api.github.com", nil
	}
	return "", fmt.Errorf("host %s sem fallback REST público", host)
}

func githubHTTPGet(rawURL string) ([]byte, http.Header, error) {
	client := &http.Client{Timeout: 25 * time.Second}
	token := strings.TrimSpace(os.Getenv("GH_TOKEN"))
	if token == "" {
		token = strings.TrimSpace(os.Getenv("GITHUB_TOKEN"))
	}
	doReq := func(bearer string) (*http.Response, []byte, error) {
		req, err := http.NewRequest(http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, nil, err
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("User-Agent", "polypod-pr-reader/1.0")
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, nil, err
		}
		defer resp.Body.Close()
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxPRJSON+1))
		if readErr != nil {
			return resp, nil, readErr
		}
		if len(body) > maxPRJSON {
			return resp, nil, fmt.Errorf("resposta do GitHub excede 4 MiB")
		}
		return resp, body, nil
	}
	resp, body, err := doReq(token)
	if err != nil {
		return nil, nil, err
	}
	// If an ambient token is expired or lacks scope on a public repository, retry unauthenticated.
	if (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) && token != "" {
		resp, body, err = doReq("")
		if err != nil {
			return nil, nil, err
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, nil, fmt.Errorf("GitHub REST HTTP %d em %s", resp.StatusCode, rawURL)
	}
	return body, resp.Header, nil
}

func readHTTPPaginatedSlice[T any](initialURL string) ([][]T, error) {
	var pages [][]T
	nextURL := initialURL
	for pageNum := 0; pageNum < maxHTTPPages && nextURL != ""; pageNum++ {
		body, headers, err := githubHTTPGet(nextURL)
		if err != nil {
			return nil, err
		}
		var items []T
		if err := json.Unmarshal(body, &items); err != nil {
			return nil, fmt.Errorf("JSON paginado inválido: %w", err)
		}
		pages = append(pages, items)
		nextURL = ""
		for _, linkHeader := range headers.Values("Link") {
			if m := linkNextRegex.FindStringSubmatch(linkHeader); len(m) == 2 {
				nextURL = m[1]
				break
			}
		}
	}
	return pages, nil
}

func fetchPRReportViaHTTP(wd, id string) (string, error) {
	if id == "" {
		return "", fmt.Errorf("informe número ou URL do PR")
	}
	var host, repo string
	var number int
	if n, err := strconv.Atoi(id); err == nil && n > 0 {
		var ok bool
		host, repo, ok = resolveGitHubRepoFromOrigin(wd)
		if !ok {
			return "", fmt.Errorf("repositório GitHub não identificado no remote origin")
		}
		number = n
	} else {
		var parseErr error
		host, repo, number, parseErr = parsePRURL(id)
		if parseErr != nil {
			return "", parseErr
		}
	}
	apiBase, err := githubAPIBase(host)
	if err != nil {
		return "", err
	}
	prBody, _, err := githubHTTPGet(fmt.Sprintf("%s/repos/%s/pulls/%d", apiBase, repo, number))
	if err != nil {
		return "", err
	}
	var pr prIdentity
	if err := json.Unmarshal(prBody, &pr); err != nil {
		return "", err
	}
	if pr.URL == "" || strings.Contains(pr.URL, "api.github.com") {
		if pr.HTMLURL != "" {
			pr.URL = pr.HTMLURL
		} else {
			pr.URL = fmt.Sprintf("https://%s/%s/pull/%d", host, repo, number)
		}
	}
	if pr.ReviewDecision == "" && pr.State != "" {
		pr.ReviewDecision = strings.ToUpper(pr.State)
	}

	var (
		general [][]prIssueComment
		reviews [][]prReview
		lines   [][]prLineComment
		files   [][]prFile
		errs    [4]error
		wg      sync.WaitGroup
	)
	wg.Add(4)
	go func() {
		defer wg.Done()
		general, errs[0] = readHTTPPaginatedSlice[prIssueComment](fmt.Sprintf("%s/repos/%s/issues/%d/comments?per_page=100", apiBase, repo, number))
	}()
	go func() {
		defer wg.Done()
		reviews, errs[1] = readHTTPPaginatedSlice[prReview](fmt.Sprintf("%s/repos/%s/pulls/%d/reviews?per_page=100", apiBase, repo, number))
	}()
	go func() {
		defer wg.Done()
		lines, errs[2] = readHTTPPaginatedSlice[prLineComment](fmt.Sprintf("%s/repos/%s/pulls/%d/comments?per_page=100", apiBase, repo, number))
	}()
	go func() {
		defer wg.Done()
		files, errs[3] = readHTTPPaginatedSlice[prFile](fmt.Sprintf("%s/repos/%s/pulls/%d/files?per_page=100", apiBase, repo, number))
	}()
	wg.Wait()
	for i := 0; i < 3; i++ {
		if errs[i] != nil {
			return "", errs[i]
		}
	}
	if pr.ReviewDecision == "" || pr.ReviewDecision == "OPEN" || pr.ReviewDecision == "CLOSED" {
		for _, page := range reviews {
			for _, r := range page {
				if r.State == "CHANGES_REQUESTED" || r.State == "APPROVED" {
					pr.ReviewDecision = r.State
				}
			}
		}
	}
	return formatPRReport(pr, general, reviews, lines, files), nil
}

// RegisterNativePRCommands replaces the prompt-only comments command with a
// reproducible read of GitHub's PR, review, and inline-comment APIs.
func RegisterNativePRCommands(reg *command.Registry) {
	reg.Register(&command.Command{
		Name: "pr_comments", Aliases: []string{"pr-comments"},
		Description: "Lista comentários gerais, revisões e comentários por linha do PR",
		Type:        command.CommandLocal, Source: command.SourceBuiltin,
		Execute: func(args []string, ctx command.CommandContext) (*command.CommandResult, error) {
			report, err := FetchPRReport(ctx.WorkingDir, args)
			if err != nil {
				return errResult(err.Error()), nil
			}
			return okResult(report), nil
		},
	})
}

func readPRPages(wd, host, endpoint string, dst any) error {
	data, err := runGHRead(wd, "api", "--hostname", host, endpoint, "--paginate", "--slurp")
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, dst); err != nil {
		return fmt.Errorf("resposta paginada inválida: %w", err)
	}
	return nil
}

func formatPRReport(pr prIdentity, general [][]prIssueComment, reviews [][]prReview, lines [][]prLineComment, files [][]prFile) string {
	var out strings.Builder
	fmt.Fprintf(&out, "PR #%d: %s\n%s\nDecisão: %s\n", pr.Number, pr.Title, pr.URL, pr.ReviewDecision)
	count := 0
	write := func(label, author, body, link string) {
		if count >= maxPRItems || out.Len() >= maxPRReport {
			return
		}
		count++
		bodyRunes := []rune(strings.TrimSpace(body))
		if len(bodyRunes) > maxPRBodyRunes {
			bodyRunes = append(bodyRunes[:maxPRBodyRunes], []rune("…")...)
		}
		fmt.Fprintf(&out, "\n%s por @%s\n%s\n", label, author, string(bodyRunes))
		if link != "" {
			fmt.Fprintln(&out, link)
		}
	}
	patches := make(map[string]string)
	for _, page := range files {
		for _, file := range page {
			patches[file.Filename] = file.Patch
		}
	}
	for _, page := range lines {
		for _, c := range page {
			line := c.Line
			if line == 0 {
				line = c.OldLine
			}
			label := fmt.Sprintf("Linha %s:%d", c.Path, line)
			if patch, ok := patches[c.Path]; ok && commentInPatch(patch, line) {
				label += " [no diff atual]"
			} else {
				label += " [trecho não disponível no patch]"
			}
			write(label, c.User.Login, c.Body, c.URL)
		}
	}
	for _, page := range reviews {
		for _, r := range page {
			if strings.TrimSpace(r.Body) != "" {
				write("Revisão "+r.State, r.User.Login, r.Body, r.URL)
			}
		}
	}
	for _, page := range general {
		for _, c := range page {
			write("Comentário", c.User.Login, c.Body, c.URL)
		}
	}
	if count == 0 {
		out.WriteString("\nNenhum comentário encontrado.\n")
	}
	if count == maxPRItems || out.Len() >= maxPRReport {
		out.WriteString("\n[Relatório abreviado.]\n")
	}
	return strings.TrimSpace(out.String())
}
