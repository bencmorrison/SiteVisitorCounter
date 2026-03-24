package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"

	"github.com/redis/go-redis/v9"
)

func setupTest(t *testing.T) *server {
	t.Helper()

	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		addr = "localhost:6379"
	}

	rdb := redis.NewClient(&redis.Options{Addr: addr})
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		t.Fatalf("could not connect to redis: %v", err)
	}

	return &server{rdb: rdb}
}

func cleanupKey(t *testing.T, s *server, siteID string) {
	t.Helper()
	if err := s.rdb.Del(context.Background(), s.redisKey(siteID)).Err(); err != nil {
		t.Logf("cleanup warning: could not delete key for %s: %v", siteID, err)
	}
}

func decodeBody(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response body: %v", err)
	}
	return body
}

func TestGetCounter_NewSite(t *testing.T) {
	s := setupTest(t)
	r := newRouter(s, []string{"*"})
	cleanupKey(t, s, "new-site")
	defer cleanupKey(t, s, "new-site")

	req := httptest.NewRequest(http.MethodGet, "/counter/new-site", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	body := decodeBody(t, w)
	if body["count"] != float64(0) {
		t.Errorf("expected count 0, got %v", body["count"])
	}
}

func TestIncrementCounter(t *testing.T) {
	s := setupTest(t)
	r := newRouter(s, []string{"*"})
	cleanupKey(t, s, "inc-site")
	defer cleanupKey(t, s, "inc-site")

	for i := 1; i <= 3; i++ {
		req := httptest.NewRequest(http.MethodPost, "/counter/inc-site/increment", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}

		body := decodeBody(t, w)
		if body["count"] != float64(i) {
			t.Errorf("expected count %d, got %v", i, body["count"])
		}
	}
}

func TestGetDoesNotIncrement(t *testing.T) {
	s := setupTest(t)
	r := newRouter(s, []string{"*"})
	cleanupKey(t, s, "get-site")
	defer cleanupKey(t, s, "get-site")

	req := httptest.NewRequest(http.MethodPost, "/counter/get-site/increment", nil)
	r.ServeHTTP(httptest.NewRecorder(), req)

	for range 2 {
		req = httptest.NewRequest(http.MethodGet, "/counter/get-site", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		body := decodeBody(t, w)
		if body["count"] != float64(1) {
			t.Errorf("expected count 1, got %v", body["count"])
		}
	}
}

func TestSitesAreIsolated(t *testing.T) {
	s := setupTest(t)
	r := newRouter(s, []string{"*"})
	cleanupKey(t, s, "site-a")
	cleanupKey(t, s, "site-b")
	defer cleanupKey(t, s, "site-a")
	defer cleanupKey(t, s, "site-b")

	for range 2 {
		req := httptest.NewRequest(http.MethodPost, "/counter/site-a/increment", nil)
		r.ServeHTTP(httptest.NewRecorder(), req)
	}
	req := httptest.NewRequest(http.MethodPost, "/counter/site-b/increment", nil)
	r.ServeHTTP(httptest.NewRecorder(), req)

	check := func(siteID string, expected float64) {
		req := httptest.NewRequest(http.MethodGet, "/counter/"+siteID, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		body := decodeBody(t, w)
		if body["count"] != expected {
			t.Errorf("%s: expected %.0f, got %v", siteID, expected, body["count"])
		}
	}

	check("site-a", 2)
	check("site-b", 1)
}

func TestInvalidSiteID(t *testing.T) {
	s := setupTest(t)
	r := newRouter(s, []string{"*"})

	// cases that reach the handler and should return 400
	badIDs := []string{
		"-bad-start",
		"has%20spaces",
		fmt.Sprintf("%065d", 0), // 65 chars, too long
	}

	for _, id := range badIDs {
		req := httptest.NewRequest(http.MethodGet, "/counter/"+id, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("siteID %q: expected 400, got %d", id, w.Code)
		}
	}

	// empty siteID is not routed by chi — expect 404
	req := httptest.NewRequest(http.MethodGet, "/counter/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("empty siteID: expected 404, got %d", w.Code)
	}
}

func TestConcurrentIncrements(t *testing.T) {
	s := setupTest(t)
	r := newRouter(s, []string{"*"})
	siteID := "concurrent-site"
	cleanupKey(t, s, siteID)
	defer cleanupKey(t, s, siteID)

	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for range goroutines {
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/counter/"+siteID+"/increment", nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Errorf("expected 200, got %d", w.Code)
			}
		}()
	}

	wg.Wait()

	req := httptest.NewRequest(http.MethodGet, "/counter/"+siteID, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	body := decodeBody(t, w)
	if body["count"] != float64(goroutines) {
		t.Errorf("expected count %d after concurrent increments, got %v", goroutines, body["count"])
	}
}

func TestCORSHeaders(t *testing.T) {
	s := setupTest(t)

	t.Run("wildcard", func(t *testing.T) {
		r := newRouter(s, []string{"*"})
		req := httptest.NewRequest(http.MethodGet, "/counter/cors-site", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if got := w.Header().Get("Access-Control-Allow-Origin"); got != "*" {
			t.Errorf("expected *, got %q", got)
		}
		if got := w.Header().Get("Vary"); got != "" {
			t.Errorf("expected no Vary header for wildcard, got %q", got)
		}
	})

	t.Run("specific origin allowed", func(t *testing.T) {
		r := newRouter(s, []string{"https://bcm.co"})
		req := httptest.NewRequest(http.MethodGet, "/counter/cors-site", nil)
		req.Header.Set("Origin", "https://bcm.co")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://bcm.co" {
			t.Errorf("expected https://bcm.co, got %q", got)
		}
		if got := w.Header().Get("Vary"); got != "Origin" {
			t.Errorf("expected Vary: Origin, got %q", got)
		}
	})

	t.Run("specific origin denied", func(t *testing.T) {
		r := newRouter(s, []string{"https://bcm.co"})
		req := httptest.NewRequest(http.MethodGet, "/counter/cors-site", nil)
		req.Header.Set("Origin", "https://evil.com")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Errorf("expected no ACAO header, got %q", got)
		}
		if got := w.Header().Get("Vary"); got != "Origin" {
			t.Errorf("expected Vary: Origin even for denied origin, got %q", got)
		}
	})

	t.Run("no origin header", func(t *testing.T) {
		r := newRouter(s, []string{"https://bcm.co"})
		req := httptest.NewRequest(http.MethodGet, "/counter/cors-site", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Errorf("expected no ACAO header, got %q", got)
		}
		if got := w.Header().Get("Vary"); got != "Origin" {
			t.Errorf("expected Vary: Origin, got %q", got)
		}
	})

	t.Run("preflight", func(t *testing.T) {
		r := newRouter(s, []string{"*"})
		req := httptest.NewRequest(http.MethodOptions, "/counter/cors-site", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusNoContent {
			t.Errorf("expected 204, got %d", w.Code)
		}
		if got := w.Header().Get("Access-Control-Allow-Methods"); got == "" {
			t.Error("expected Access-Control-Allow-Methods header")
		}
	})
}

func TestParseOriginsText(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name:  "simple list",
			input: "https://a.com\nhttps://b.com\n",
			want:  []string{"https://a.com", "https://b.com"},
		},
		{
			name:  "comments ignored",
			input: "# comment\nhttps://a.com\n# another\n",
			want:  []string{"https://a.com"},
		},
		{
			name:  "blank lines ignored",
			input: "\nhttps://a.com\n\nhttps://b.com\n\n",
			want:  []string{"https://a.com", "https://b.com"},
		},
		{
			name:  "whitespace trimmed",
			input: "  https://a.com  \n\t https://b.com\t\n",
			want:  []string{"https://a.com", "https://b.com"},
		},
		{
			name:  "empty input",
			input: "",
			want:  nil,
		},
		{
			name:  "only comments and blanks",
			input: "# comment\n\n# another\n",
			want:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseOriginsText(tt.input)
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("index %d: got %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}
