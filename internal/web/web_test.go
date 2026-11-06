package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// ddgHTMLFixture is a minimal snippet of the HTML structure that
// DuckDuckGo's html.duckduckgo.com endpoint returns. The parser keys off
// the `result__body`, `result__a`, and `result__snippet` class names.
const ddgHTMLFixture = `
<html><body>
<div class="results">

  <div class="result result__body">
    <h2 class="result__title">
      <a class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2Ffoo&amp;rut=abc">
        First Result Title
      </a>
    </h2>
    <a class="result__snippet" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2Ffoo">
      This is the first snippet describing the page content.
    </a>
  </div>

  <div class="result result__body">
    <h2 class="result__title">
      <a class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.org%2Fbar">
        Second Result
      </a>
    </h2>
    <a class="result__snippet" href="#">
      Second snippet text here.
    </a>
  </div>

  <div class="result result__body">
    <h2 class="result__title">
      <a class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fthird.example.net%2Fpage">
        Third Result
      </a>
    </h2>
    <a class="result__snippet" href="#">
      Third snippet.
    </a>
  </div>

</div>
</body></html>
`

// ---------- parseSearchResults ----------

func TestParseSearchResults_ExtractsAllResults(t *testing.T) {
	results := parseSearchResults(ddgHTMLFixture, 10)
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d: %+v", len(results), results)
	}

	want := []struct {
		title   string
		url     string
		snippet string
	}{
		{"First Result Title", "https://example.com/foo", "This is the first snippet describing the page content."},
		{"Second Result", "https://example.org/bar", "Second snippet text here."},
		{"Third Result", "https://third.example.net/page", "Third snippet."},
	}

	for i, w := range want {
		got := results[i]
		if got.Title != w.title {
			t.Errorf("result[%d].Title = %q, want %q", i, got.Title, w.title)
		}
		if got.URL != w.url {
			t.Errorf("result[%d].URL = %q, want %q", i, got.URL, w.url)
		}
		if got.Snippet != w.snippet {
			t.Errorf("result[%d].Snippet = %q, want %q", i, got.Snippet, w.snippet)
		}
	}
}

func TestParseSearchResults_RespectsMaxResults(t *testing.T) {
	results := parseSearchResults(ddgHTMLFixture, 2)
	if len(results) != 2 {
		t.Fatalf("expected 2 results when maxResults=2, got %d", len(results))
	}
	if results[0].Title != "First Result Title" {
		t.Errorf("first title = %q, want %q", results[0].Title, "First Result Title")
	}
	if results[1].Title != "Second Result" {
		t.Errorf("second title = %q, want %q", results[1].Title, "Second Result")
	}
}

func TestParseSearchResults_EmptyHTML(t *testing.T) {
	results := parseSearchResults("<html><body>nothing here</body></html>", 5)
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestParseSearchResults_DefaultsToFiveWhenMaxResultsZero(t *testing.T) {
	// Build a fixture with more than 5 results to verify the default cap.
	var sb strings.Builder
	sb.WriteString("<html><body>")
	for i := 0; i < 8; i++ {
		sb.WriteString(`<div class="result result__body">`)
		sb.WriteString(`<a class="result__a" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fe.com">Title</a>`)
		sb.WriteString(`<a class="result__snippet" href="#">Snip</a>`)
		sb.WriteString(`</div>`)
	}
	sb.WriteString("</body></html>")

	results := parseSearchResults(sb.String(), 0)
	if len(results) != 5 {
		t.Errorf("expected default cap of 5, got %d", len(results))
	}
}

// ---------- extractDDGURL ----------

func TestExtractDDGURL(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "uddg_redirect",
			in:   "//duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2Fpage",
			want: "https://example.com/page",
		},
		{
			name: "uddg_with_trailing_params",
			in:   "//duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2Fpage&rut=abc",
			want: "https://example.com/page",
		},
		{
			name: "direct_url_passthrough",
			in:   "https://plain.example.com/",
			want: "https://plain.example.com/",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractDDGURL(tc.in)
			if got != tc.want {
				t.Errorf("extractDDGURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// ---------- SafeClient ----------

func TestSafeClient_Get_BlocksLoopbackByDefault(t *testing.T) {
	// httptest.NewServer listens on 127.0.0.1 - the default SafeClient must refuse.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("hello"))
	}))
	defer srv.Close()

	client := NewSafeClient(5 * time.Second)
	_, err := client.Get(context.Background(), srv.URL)
	if err == nil {
		t.Fatalf("expected SafeClient.Get to block loopback %s, got nil error", srv.URL)
	}
	if !strings.Contains(err.Error(), "SSRF") && !strings.Contains(err.Error(), "loopback") {
		t.Errorf("expected SSRF/loopback error, got: %v", err)
	}
}

func TestSafeClient_Get_AllowsLoopbackViaAllowlist(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ua := r.Header.Get("User-Agent"); ua == "" {
			t.Errorf("expected User-Agent to be set, got empty")
		}
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("hello from mock"))
	}))
	defer srv.Close()

	// Parse host out of the test server URL so the allowlist matches it exactly.
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse srv URL: %v", err)
	}
	host := u.Hostname() // "127.0.0.1"

	client := NewSafeClientWithAllowlist(5*time.Second, []string{host})
	body, err := client.Get(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("unexpected error from allowlisted host: %v", err)
	}
	if string(body) != "hello from mock" {
		t.Errorf("body = %q, want %q", string(body), "hello from mock")
	}
}

func TestSafeClient_GetString_AllowlistedHost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("string-body"))
	}))
	defer srv.Close()

	u, _ := url.Parse(srv.URL)
	client := NewSafeClientWithAllowlist(5*time.Second, []string{u.Hostname()})

	s, err := client.GetString(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("GetString: %v", err)
	}
	if s != "string-body" {
		t.Errorf("GetString = %q, want %q", s, "string-body")
	}
}

func TestSafeClient_Get_HTTPErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer srv.Close()

	u, _ := url.Parse(srv.URL)
	client := NewSafeClientWithAllowlist(5*time.Second, []string{u.Hostname()})

	_, err := client.Get(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("expected http error for 404, got nil")
	}
	if !strings.Contains(err.Error(), "404") && !strings.Contains(err.Error(), "http error") {
		t.Errorf("expected 404/http error, got: %v", err)
	}
}

func TestSafeClient_ValidateURL_RejectsUnsupportedSchemes(t *testing.T) {
	client := NewSafeClient(5 * time.Second)
	cases := []string{
		"file:///etc/passwd",
		"ftp://example.com/",
		"gopher://example.com/",
		"javascript:alert(1)",
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			_, err := client.Get(context.Background(), raw)
			if err == nil {
				t.Errorf("expected error for scheme %q, got nil", raw)
			}
		})
	}
}

func TestSafeClient_ValidateURL_RejectsLocalhostAlias(t *testing.T) {
	client := NewSafeClient(5 * time.Second)
	_, err := client.Get(context.Background(), "http://localhost:8080/")
	if err == nil {
		t.Fatal("expected localhost to be blocked, got nil")
	}
	if !strings.Contains(err.Error(), "SSRF") && !strings.Contains(err.Error(), "loopback") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestSafeClient_ValidateURL_RejectsMetadataIP(t *testing.T) {
	client := NewSafeClient(5 * time.Second)
	_, err := client.Get(context.Background(), "http://169.254.169.254/latest/meta-data/")
	if err == nil {
		t.Fatal("expected metadata IP to be blocked, got nil")
	}
}

func TestSafeClient_ValidateURL_RejectsPrivateIP(t *testing.T) {
	client := NewSafeClient(5 * time.Second)
	cases := []string{
		"http://10.0.0.1/",
		"http://192.168.1.1/",
		"http://172.16.0.1/",
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			_, err := client.Get(context.Background(), raw)
			if err == nil {
				t.Errorf("expected %s to be blocked, got nil", raw)
			}
		})
	}
}

// ---------- SearchCached ----------

func TestSearchCached_ReturnsCachedResults(t *testing.T) {
	ClearSearchCache()

	query := "golang testing"
	maxResults := 3
	key := normalizeQuery(query, maxResults)

	// Pre-seed the cache so SearchCached never touches the network.
	seeded := []SearchResult{
		{Title: "Cached One", URL: "https://a.example/", Snippet: "snippet one"},
		{Title: "Cached Two", URL: "https://b.example/", Snippet: "snippet two"},
	}
	getSearchCache().Set(key, seeded)

	got, err := SearchCached(context.Background(), query, maxResults)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != len(seeded) {
		t.Fatalf("got %d results, want %d", len(got), len(seeded))
	}
	for i := range seeded {
		if got[i] != seeded[i] {
			t.Errorf("result[%d] = %+v, want %+v", i, got[i], seeded[i])
		}
	}
}

func TestSearchCached_ReturnsCopyNotSharedSlice(t *testing.T) {
	ClearSearchCache()

	query := "copy test"
	key := normalizeQuery(query, 2)
	getSearchCache().Set(key, []SearchResult{
		{Title: "Original", URL: "https://orig.example/", Snippet: "s"},
	})

	got, err := SearchCached(context.Background(), query, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 result, got %d", len(got))
	}

	// Mutate the returned slice; the cache must not be affected.
	got[0].Title = "Mutated"

	again, err := SearchCached(context.Background(), query, 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if again[0].Title != "Original" {
		t.Errorf("cache was mutated through returned slice: got %q", again[0].Title)
	}
}

func TestSearchCached_NormalizesQueryCasingAndWhitespace(t *testing.T) {
	ClearSearchCache()

	// Two queries that should collapse to the same cache key.
	key := normalizeQuery("HELLO World", 5)
	keySame := normalizeQuery("  hello world  ", 5)
	if key != keySame {
		t.Fatalf("normalizeQuery did not collapse casing/whitespace: %q vs %q", key, keySame)
	}

	getSearchCache().Set(key, []SearchResult{
		{Title: "Normalized Hit", URL: "https://n.example/", Snippet: "n"},
	})

	got, err := SearchCached(context.Background(), "  hello world  ", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].Title != "Normalized Hit" {
		t.Errorf("normalization cache hit failed: %+v", got)
	}
}

// ---------- Cache stats / admin ----------

func TestSearchCacheStats_IncrementsOnHitAndMiss(t *testing.T) {
	ClearSearchCache()

	// After Clear, stats start from whatever the backing cache reports.
	hitsBefore, missesBefore, _, _ := SearchCacheStats()

	key := normalizeQuery("stats-query", 3)
	getSearchCache().Set(key, []SearchResult{{Title: "s", URL: "https://s.example/"}})

	// Direct cache Get produces a hit.
	if _, ok := getSearchCache().Get(key); !ok {
		t.Fatal("expected cache hit after Set")
	}
	// A direct Get on a missing key produces a miss.
	if _, ok := getSearchCache().Get("definitely-not-there"); ok {
		t.Fatal("expected miss for unknown key")
	}

	hitsAfter, missesAfter, _, hitRate := SearchCacheStats()
	if hitsAfter <= hitsBefore {
		t.Errorf("expected hits to increase: before=%d after=%d", hitsBefore, hitsAfter)
	}
	if missesAfter <= missesBefore {
		t.Errorf("expected misses to increase: before=%d after=%d", missesBefore, missesAfter)
	}
	if hitRate < 0 || hitRate > 1 {
		t.Errorf("hit rate out of [0,1]: %v", hitRate)
	}
}

func TestSearchCacheSize_AndClear(t *testing.T) {
	ClearSearchCache()

	if got := SearchCacheSize(); got != 0 {
		t.Fatalf("expected empty cache after clear, got size %d", got)
	}

	cache := getSearchCache()
	cache.Set(normalizeQuery("a", 1), []SearchResult{{Title: "A"}})
	cache.Set(normalizeQuery("b", 1), []SearchResult{{Title: "B"}})
	cache.Set(normalizeQuery("c", 1), []SearchResult{{Title: "C"}})

	if got := SearchCacheSize(); got != 3 {
		t.Errorf("expected size 3, got %d", got)
	}

	ClearSearchCache()
	if got := SearchCacheSize(); got != 0 {
		t.Errorf("expected size 0 after ClearSearchCache, got %d", got)
	}
}

func TestPruneExpiredSearches_NoOpOnFreshEntries(t *testing.T) {
	ClearSearchCache()
	getSearchCache().Set(normalizeQuery("fresh", 1), []SearchResult{{Title: "fresh"}})

	// Entries have a 10-minute TTL; Prune should remove nothing.
	n := PruneExpiredSearches()
	if n != 0 {
		t.Errorf("expected 0 expired entries, got %d", n)
	}
	if SearchCacheSize() != 1 {
		t.Errorf("expected size 1 after Prune, got %d", SearchCacheSize())
	}
}

// ---------- normalizeQuery / buildCacheKey ----------

func TestNormalizeQuery_DifferentMaxResultsProduceDifferentKeys(t *testing.T) {
	k1 := normalizeQuery("same query", 5)
	k2 := normalizeQuery("same query", 10)
	if k1 == k2 {
		t.Errorf("expected different keys for different maxResults, both=%q", k1)
	}
}

func TestBuildCacheKey_Deterministic(t *testing.T) {
	a := buildCacheKey("foo", 3)
	b := buildCacheKey("foo", 3)
	if a != b {
		t.Errorf("buildCacheKey is not deterministic: %q vs %q", a, b)
	}
}
