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

func corsMiddleware(allowedOrigin string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", allowedOrigin)
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

func newRouter(s *server, allowedOrigin string) *chi.Mux {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(corsMiddleware(allowedOrigin))
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

	allowedOrigin := os.Getenv("ALLOWED_ORIGIN")
	if allowedOrigin == "" {
		allowedOrigin = "*"
	}

	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}

	srv := &http.Server{
		Addr:         addr,
		Handler:      newRouter(&server{rdb: rdb}, allowedOrigin),
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
