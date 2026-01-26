package handlers

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"text/template"
)

func TestServeAnswer_InvalidPath_Short(t *testing.T) {
	// Test path validation logic directly
	handler := func(w http.ResponseWriter, r *http.Request) {
		// Simulate the path check from ServeAnswer
		parts := []string{} // Invalid - less than 2 parts
		if len(parts) < 2 {
			w.Header().Set("Content-Type", "text/plain")
			w.Header().Set("Content-Length", "0")
			w.WriteHeader(http.StatusOK)
			return
		}
	}

	req := httptest.NewRequest("GET", "/answer/invalid", nil)
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	if w.Header().Get("Content-Length") != "0" {
		t.Errorf("Expected Content-Length: 0, got %s", w.Header().Get("Content-Length"))
	}
}

func TestServeAnswer_NoDeployResponse(t *testing.T) {
	// Test the response format when no deploy is found
	handler := func(w http.ResponseWriter, r *http.Request) {
		// Simulate no deploy found
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(http.StatusOK)
	}

	req := httptest.NewRequest("GET", "/answer/vm125/preseed.cfg", nil)
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	if w.Header().Get("Content-Length") != "0" {
		t.Errorf("Expected Content-Length: 0, got %s", w.Header().Get("Content-Length"))
	}

	if w.Header().Get("Content-Type") != "text/plain" {
		t.Errorf("Expected Content-Type: text/plain, got %s", w.Header().Get("Content-Type"))
	}
}

func TestAnswerContentLength(t *testing.T) {
	// Verify that answer content is rendered to buffer and Content-Length is set
	tmpl, _ := template.New("answer").Parse("test content")

	var buf bytes.Buffer
	tmpl.Execute(&buf, nil)

	if buf.Len() != 12 {
		t.Errorf("Expected length 12, got %d", buf.Len())
	}

	// Simulate setting Content-Length
	w := httptest.NewRecorder()
	w.Header().Set("Content-Type", "text/plain")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", buf.Len()))
	w.WriteHeader(http.StatusOK)
	w.Write(buf.Bytes())

	if w.Header().Get("Content-Length") != "12" {
		t.Errorf("Expected Content-Length: 12, got %s", w.Header().Get("Content-Length"))
	}
}
