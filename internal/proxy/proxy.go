// Package proxy implements localrouter's HTTP reverse proxy in front of a
// remote Ollama instance.
//
// The proxy is mostly transparent: requests are forwarded with
// httputil.NewSingleHostReverseProxy, including chunked streaming responses
// (Ollama's /api/chat and /api/generate use this). Two endpoints get
// intercepted before forwarding:
//
//   - POST /api/chat
//   - POST /api/generate
//
// For those, the body is read into memory to extract the "model" field,
// then restored via io.NopCloser(bytes.NewReader(...)) so the reverse proxy
// sees an identical request. If auto-pull is enabled and the model is not
// installed on the remote, we synchronously call /api/pull and only resume
// forwarding once it succeeds.
package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"strings"
	"sync"
	"time"

	"github.com/fede-h/localrouter/internal/ollama"
)

// Options configures the reverse proxy.
type Options struct {
	// Remote is the Ollama client backing the proxy.
	Remote *ollama.Client

	// AutoPull enables on-demand pulling for intercepted requests.
	AutoPull bool

	// PullTimeout caps each /api/pull call.
	PullTimeout time.Duration

	// Logger is used for proxy-level events. Required.
	Logger *log.Logger
}

// Server is a thin wrapper over a configured http.Server. Construct via New
// and serve with Run.
type Server struct {
	opts  Options
	rp    *httputil.ReverseProxy
	mux   *http.ServeMux
	pulls *pullGuard
}

// New constructs a Server. It does not bind to any port — call Run for that.
func New(opts Options) (*Server, error) {
	if opts.Remote == nil {
		return nil, errors.New("proxy: Remote is required")
	}
	if opts.Logger == nil {
		return nil, errors.New("proxy: Logger is required")
	}
	if opts.PullTimeout <= 0 {
		opts.PullTimeout = 30 * time.Minute
	}

	rp := httputil.NewSingleHostReverseProxy(opts.Remote.BaseURL)

	// FlushInterval=-1 forces immediate flushes on every write, which is
	// what Ollama's streaming JSON responses need to feel responsive.
	rp.FlushInterval = -1

	// Custom error handler so the upstream being down returns 502 instead
	// of letting the default handler write a Go-style error page.
	rp.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		opts.Logger.Printf("upstream error: %s %s: %v", r.Method, r.URL.Path, err)
		writeJSONError(w, http.StatusBadGateway, fmt.Sprintf("upstream unreachable: %v", err))
	}

	// Preserve the original director (rewrites scheme/host/path) and tack
	// on a Host header rewrite so virtual-hosted Ollama setups work too.
	origDirector := rp.Director
	rp.Director = func(req *http.Request) {
		origDirector(req)
		req.Host = opts.Remote.BaseURL.Host
	}

	s := &Server{
		opts:  opts,
		rp:    rp,
		mux:   http.NewServeMux(),
		pulls: newPullGuard(),
	}
	s.registerRoutes()
	return s, nil
}

func (s *Server) registerRoutes() {
	// Health endpoint local to the proxy — lets `localrouter status`
	// confirm it's talking to us and not a stray Ollama on the same port.
	s.mux.HandleFunc("/__localrouter/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":        true,
			"service":   "localrouter",
			"remote":    s.opts.Remote.BaseURL.String(),
			"auto_pull": s.opts.AutoPull,
		})
	})

	// Intercepted endpoints. Everything else falls through to the catch-
	// all reverse proxy below.
	s.mux.HandleFunc("/api/chat", s.handleIntercepted("/api/chat"))
	s.mux.HandleFunc("/api/generate", s.handleIntercepted("/api/generate"))

	// Catch-all: forward verbatim.
	s.mux.Handle("/", s.rp)
}

// Run binds to listenAddr and serves until ctx is cancelled or an error
// occurs. ctx cancellation triggers a graceful shutdown with a short
// deadline.
func (s *Server) Run(ctx context.Context, listenAddr string) error {
	srv := &http.Server{
		Addr:    listenAddr,
		Handler: s.mux,
		// No ReadTimeout / WriteTimeout: Ollama streaming responses can
		// take many minutes, and the upstream already governs total
		// duration via the request context.
		IdleTimeout: 120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		s.opts.Logger.Printf("listening on %s -> %s", listenAddr, s.opts.Remote.BaseURL)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		return ctx.Err()
	}
}

// handleIntercepted returns a handler for /api/chat or /api/generate that
// peeks at the "model" field, ensures the remote has it (auto-pulling on
// demand if configured), restores the body, and hands off to the reverse
// proxy.
func (s *Server) handleIntercepted(label string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Only POSTs carry a model in the body. GET/OPTIONS pass through.
		if r.Method != http.MethodPost {
			s.rp.ServeHTTP(w, r)
			return
		}

		// Read the entire body so we can inspect it. Ollama chat/generate
		// request payloads are kilobytes at most.
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 8<<20)) // 8MB ceiling
		if err != nil {
			s.opts.Logger.Printf("%s: read body failed: %v", label, err)
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("read request body: %v", err))
			return
		}
		// CRITICAL: Restore the body so the reverse proxy can re-read it.
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))

		model := extractModel(body)
		if model == "" {
			// No model field — let Ollama itself return the error, we
			// don't want to second-guess the API shape.
			s.opts.Logger.Printf("%s: request has no model field, forwarding as-is", label)
			s.rp.ServeHTTP(w, r)
			return
		}

		// Cheap path: if we can confirm the model is present, forward
		// immediately. Tags() is one HTTP call to the remote.
		probeCtx, probeCancel := context.WithTimeout(r.Context(), 10*time.Second)
		has, err := s.opts.Remote.HasModel(probeCtx, model)
		probeCancel()
		if err != nil {
			s.opts.Logger.Printf("%s: tags probe failed: %v", label, err)
			writeJSONError(w, http.StatusBadGateway, fmt.Sprintf("could not check remote /api/tags: %v", err))
			return
		}
		if has {
			s.rp.ServeHTTP(w, r)
			return
		}

		// Model missing. If auto-pull is off, surface a precise error
		// instead of letting Ollama return an opaque 404 mid-stream.
		if !s.opts.AutoPull {
			s.opts.Logger.Printf("%s: model %q missing on remote and auto-pull disabled", label, model)
			writeJSONError(w, http.StatusNotFound, fmt.Sprintf("model %q not on remote and --auto-pull is off", model))
			return
		}

		// Pull. Coalesce concurrent requests for the same model so we
		// don't issue N parallel pulls of llama3:8b under load.
		pullCtx, pullCancel := context.WithTimeout(r.Context(), s.opts.PullTimeout)
		defer pullCancel()
		if err := s.pulls.do(model, func() error {
			s.opts.Logger.Printf("%s: model %q missing, pulling…", label, model)
			return s.opts.Remote.Pull(pullCtx, model, func(msg string) {
				s.opts.Logger.Printf("pull[%s]: %s", model, msg)
			})
		}); err != nil {
			s.opts.Logger.Printf("%s: pull of %q failed: %v", label, model, err)
			writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("auto-pull failed for %q: %v", model, err))
			return
		}
		s.opts.Logger.Printf("%s: pull of %q done, forwarding", label, model)

		// Body was already restored above; hand off to the reverse proxy.
		s.rp.ServeHTTP(w, r)
	}
}

// extractModel returns the "model" field from an Ollama chat/generate
// request body, or "" if the body doesn't decode or omits the field.
//
// We deliberately tolerate trailing garbage and unknown fields — Ollama's
// schema isn't ours to gatekeep.
func extractModel(body []byte) string {
	var probe struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return ""
	}
	return strings.TrimSpace(probe.Model)
}

// writeJSONError emits {"error": "..."} with the given status. Ollama
// clients tend to surface this string verbatim, so keep messages short and
// actionable.
func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// pullGuard de-duplicates concurrent pulls of the same model.
//
// Two requests for "llama3:8b" arriving 100ms apart would otherwise both
// trigger /api/pull. The second waits on the first's result instead.
type pullGuard struct {
	mu sync.Mutex
	m  map[string]*pullCall
}

type pullCall struct {
	done chan struct{}
	err  error
}

func newPullGuard() *pullGuard {
	return &pullGuard{m: make(map[string]*pullCall)}
}

func (g *pullGuard) do(key string, fn func() error) error {
	g.mu.Lock()
	if c, ok := g.m[key]; ok {
		g.mu.Unlock()
		<-c.done
		return c.err
	}
	c := &pullCall{done: make(chan struct{})}
	g.m[key] = c
	g.mu.Unlock()

	c.err = fn()
	close(c.done)

	g.mu.Lock()
	delete(g.m, key)
	g.mu.Unlock()

	return c.err
}
