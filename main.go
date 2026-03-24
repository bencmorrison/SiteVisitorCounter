package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/redis/go-redis/v9"
)

var validSiteID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9-]{0,62}$`)

type server struct {
	rdb *redis.Client
}

func (s *server) redisKey(siteID string) string {
	return "counter:" + siteID
}

func (s *server) getCounter(w http.ResponseWriter, r *http.Request) {
	siteID := chi.URLParam(r, "siteID")
	if !validSiteID.MatchString(siteID) {
		http.Error(w, "invalid site_id", http.StatusBadRequest)
		return
	}

	val, err := s.rdb.Get(r.Context(), s.redisKey(siteID)).Int64()
	if err == redis.Nil {
		val = 0
	} else if err != nil {
		log.Printf("redis get error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"site_id": siteID, "count": val})
}

func (s *server) incrementCounter(w http.ResponseWriter, r *http.Request) {
	siteID := chi.URLParam(r, "siteID")
	if !validSiteID.MatchString(siteID) {
		http.Error(w, "invalid site_id", http.StatusBadRequest)
		return
	}

	val, err := s.rdb.Incr(r.Context(), s.redisKey(siteID)).Result()
	if err != nil {
		log.Printf("redis incr error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"site_id": siteID, "count": val})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(v); err != nil {
		log.Printf("json encode error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(buf.Bytes())
}

func parseOriginsText(text string) []string {
	var origins []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			origins = append(origins, line)
		}
	}
	return origins
}

func corsMiddleware(allowedOrigins []string) func(http.Handler) http.Handler {
	wildcard := slices.Contains(allowedOrigins, "*")
	originSet := make(map[string]bool)
	if !wildcard {
		for _, o := range allowedOrigins {
			originSet[o] = true
		}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if wildcard {
				w.Header().Set("Access-Control-Allow-Origin", "*")
			} else {
				// Always set Vary so caches don't serve the wrong response
				// regardless of whether the origin is allowed or not.
				w.Header().Add("Vary", "Origin")
				origin := r.Header.Get("Origin")
				if originSet[origin] {
					w.Header().Set("Access-Control-Allow-Origin", origin)
				}
			}
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func newRouter(s *server, allowedOrigins []string) *chi.Mux {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(corsMiddleware(allowedOrigins))
	r.Get("/counter/{siteID}", s.getCounter)
	r.Post("/counter/{siteID}/increment", s.incrementCounter)
	return r
}

func main() {
	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "localhost:6379"
	}

	rdb := redis.NewClient(&redis.Options{Addr: redisAddr})

	pingCtx, pingCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer pingCancel()
	if err := rdb.Ping(pingCtx).Err(); err != nil {
		log.Fatalf("could not connect to redis: %v", err)
	}

	var allowedOrigins []string
	if filePath := os.Getenv("ALLOWED_ORIGINS_FILE"); filePath != "" {
		data, err := os.ReadFile(filePath)
		if err != nil {
			log.Fatalf("could not read ALLOWED_ORIGINS_FILE: %v", err)
		}
		allowedOrigins = append(allowedOrigins, parseOriginsText(string(data))...)
	}
	if origin := os.Getenv("ALLOWED_ORIGINS"); origin != "" {
		for _, o := range strings.Split(origin, ",") {
			if o = strings.TrimSpace(o); o != "" {
				allowedOrigins = append(allowedOrigins, o)
			}
		}
	}
	if len(allowedOrigins) == 0 {
		log.Println("warning: ALLOWED_ORIGINS and ALLOWED_ORIGINS_FILE are not set, defaulting to * (all origins allowed)")
		allowedOrigins = []string{"*"}
	}

	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}

	srv := &http.Server{
		Addr:         addr,
		Handler:      newRouter(&server{rdb: rdb}, allowedOrigins),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		log.Printf("listening on %s", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server error: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("shutting down...")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("forced shutdown: %v", err)
	}
	log.Println("done")
}
