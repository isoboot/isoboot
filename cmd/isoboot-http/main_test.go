package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestResponseWriter_WriteHeader(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
	}{
		{"OK", http.StatusOK},
		{"NotFound", http.StatusNotFound},
		{"InternalServerError", http.StatusInternalServerError},
		{"BadGateway", http.StatusBadGateway},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			rw := &responseWriter{ResponseWriter: rec, status: http.StatusOK}

			rw.WriteHeader(tt.statusCode)

			if rw.status != tt.statusCode {
				t.Errorf("expected status %d, got %d", tt.statusCode, rw.status)
			}
			if rec.Code != tt.statusCode {
				t.Errorf("expected recorder code %d, got %d", tt.statusCode, rec.Code)
			}
		})
	}
}

func TestResponseWriter_DefaultStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := &responseWriter{ResponseWriter: rec, status: http.StatusOK}

	// Write body without calling WriteHeader - status should remain default
	rw.Write([]byte("test"))

	if rw.status != http.StatusOK {
		t.Errorf("expected default status %d, got %d", http.StatusOK, rw.status)
	}
}

func TestLoggingMiddleware_PassesThrough(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte("created"))
	})

	wrapped := loggingMiddleware(handler)

	req := httptest.NewRequest("POST", "/test", nil)
	rec := httptest.NewRecorder()

	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Errorf("expected status %d, got %d", http.StatusCreated, rec.Code)
	}
	if rec.Body.String() != "created" {
		t.Errorf("expected body 'created', got %q", rec.Body.String())
	}
}

func TestLoggingMiddleware_CapturesStatusCode(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
	}{
		{"OK", http.StatusOK},
		{"NotFound", http.StatusNotFound},
		{"BadRequest", http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
			})

			wrapped := loggingMiddleware(handler)

			req := httptest.NewRequest("GET", "/test", nil)
			rec := httptest.NewRecorder()

			wrapped.ServeHTTP(rec, req)

			if rec.Code != tt.statusCode {
				t.Errorf("expected status %d, got %d", tt.statusCode, rec.Code)
			}
		})
	}
}

func TestPathTraversalMiddleware_BlocksTraversal(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	wrapped := pathTraversalMiddleware(handler)

	tests := []struct {
		name       string
		path       string
		wantStatus int
	}{
		{"normal path", "/iso/content/foo/bar", http.StatusOK},
		{"root path", "/", http.StatusOK},
		{"healthz", "/healthz", http.StatusOK},
		{"dotdot segment", "/iso/content/../etc/passwd", http.StatusBadRequest},
		{"dotdot at start", "/../etc/passwd", http.StatusBadRequest},
		{"dotdot at end", "/foo/bar/..", http.StatusBadRequest},
		{"bare dotdot", "/..", http.StatusBadRequest},
		{"backslash traversal", "/iso/content\\..\\etc\\passwd", http.StatusBadRequest},
		{"single dot is fine", "/foo/./bar", http.StatusOK},
		// URL-encoded variants: Go decodes %2e to "." before r.URL.Path,
		// so these are caught by the same segment check.
		{"url-encoded dotdot", "/foo/%2e%2e/bar", http.StatusBadRequest},
		{"url-encoded mixed case", "/foo/%2E%2E/bar", http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", tt.path, nil)
			rec := httptest.NewRecorder()
			wrapped.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("path %q: got status %d, want %d", tt.path, rec.Code, tt.wantStatus)
			}
		})
	}
}

func TestLoggingMiddleware_DefaultStatusOK(t *testing.T) {
	// Handler that writes body but doesn't call WriteHeader
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})

	wrapped := loggingMiddleware(handler)

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	wrapped.ServeHTTP(rec, req)

	// Default status should be 200 OK
	if rec.Code != http.StatusOK {
		t.Errorf("expected default status %d, got %d", http.StatusOK, rec.Code)
	}
}
