package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

type localSocket struct {
	server *http.Server
	done   chan struct{}
}

// The daemon lock protects this stable socket; only the worker opens the database.
func openLocalSocket(ctx context.Context, q *localQueue, stop context.CancelFunc) (*localSocket, error) {
	path := filepath.Join(q.dir, "control.sock")
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("refusing to replace non-socket: %s", path)
		}
		if err := os.Remove(path); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0600); err != nil {
		listener.Close()
		return nil, err
	}
	routes := q.routes()
	routes.HandleFunc("POST /daemon/stop", func(w http.ResponseWriter, r *http.Request) {
		// Cancellation requests shutdown; the lifecycle owner closes the server.
		// CancelFunc is safe to call repeatedly or from concurrent handlers.
		defer stop()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(struct {
			OK bool `json:"ok"`
		}{OK: true})
	})
	s := &localSocket{server: &http.Server{
		Handler: routes, ReadTimeout: 5 * time.Second,
		WriteTimeout: 15 * time.Second, IdleTimeout: 30 * time.Second,
		BaseContext: func(net.Listener) context.Context { return ctx },
	}, done: make(chan struct{})}
	go func() {
		defer close(s.done)
		s.server.Serve(listener)
	}()
	return s, nil
}

func (s *localSocket) Close() {
	// Join handlers before the caller closes the database.
	s.server.Shutdown(context.Background())
	<-s.done
}

func (q *localQueue) routes() *http.ServeMux {
	mux := http.NewServeMux()
	register := func(pattern string, handle func(*http.Request) (any, error)) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			if r.Context().Err() != nil {
				http.Error(w, "worker is stopping", http.StatusServiceUnavailable)
				return
			}
			// A maximum-size command can expand sixfold when JSON-escaped.
			r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
			result, err := handle(r)
			if err != nil {
				status := http.StatusBadRequest
				if errors.Is(err, sql.ErrNoRows) {
					status = http.StatusNotFound
				}
				http.Error(w, err.Error(), status)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(result)
		})
	}
	register("GET /health", func(r *http.Request) (any, error) {
		return struct {
			Status string `json:"status"`
		}{Status: "ok"}, nil
	})
	register("POST /jobs", func(r *http.Request) (any, error) {
		var body struct {
			Command []string `json:"command"`
		}
		if err := decodeLocalBody(r, &body); err != nil {
			return nil, err
		}
		return q.submit(body.Command)
	})
	register("GET /jobs", func(r *http.Request) (any, error) {
		limit, offset := 100, 0
		for name, target := range map[string]*int{"limit": &limit, "offset": &offset} {
			if value := r.URL.Query().Get(name); value != "" {
				n, err := strconv.Atoi(value)
				if err != nil {
					return nil, fmt.Errorf("invalid pagination")
				}
				*target = n
			}
		}
		if limit < 1 || limit > 100 || offset < 0 {
			return nil, fmt.Errorf("invalid pagination")
		}
		jobs, err := q.list(limit, offset)
		return map[string]any{"jobs": jobs}, err
	})
	register("GET /jobs/latest", func(r *http.Request) (any, error) { return q.latest(r.URL.Query().Get("kind")) })
	register("GET /jobs/{id}", func(r *http.Request) (any, error) { return q.get(r.PathValue("id")) })
	register("DELETE /jobs/{id}", func(r *http.Request) (any, error) { return nil, q.remove(r.PathValue("id")) })
	register("POST /jobs/{id}/cancel", func(r *http.Request) (any, error) { return nil, q.requestCancel(r.PathValue("id")) })
	register("POST /jobs/{id}/urgent", func(r *http.Request) (any, error) { return nil, q.reorder(r.PathValue("id"), "") })
	register("POST /jobs/clear", func(r *http.Request) (any, error) { return nil, q.clear() })
	register("POST /jobs/remove-all", func(r *http.Request) (any, error) { return q.removeAll() })
	for _, action := range []string{"retry", "urgent", "remove"} {
		register("POST /jobs/"+action, func(r *http.Request) (any, error) {
			var body struct {
				IDs []string `json:"ids"`
			}
			if err := decodeLocalBody(r, &body); err != nil {
				return nil, err
			}
			return q.batchJobs(body.IDs, action)
		})
	}
	register("POST /jobs/retry-all", func(r *http.Request) (any, error) { return q.retry("") })
	register("POST /jobs/{id}/retry", func(r *http.Request) (any, error) { return q.retry(r.PathValue("id")) })
	register("POST /jobs/swap", func(r *http.Request) (any, error) {
		var body struct {
			First  string `json:"first"`
			Second string `json:"second"`
		}
		if err := decodeLocalBody(r, &body); err != nil {
			return nil, err
		}
		if body.First == "" || body.Second == "" {
			return nil, fmt.Errorf("swap requires two job IDs")
		}
		return nil, q.reorder(body.First, body.Second)
	})
	return mux
}

func decodeLocalBody(r *http.Request, body any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(body); err != nil {
		return err
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return fmt.Errorf("expected one JSON object")
	}
	return nil
}
