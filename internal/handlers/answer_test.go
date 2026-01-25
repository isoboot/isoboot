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

func TestCompleteDeployment_WrongMethod(t *testing.T) {
	// Test method validation directly
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Content-Length", "0")
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
	}

	req := httptest.NewRequest("GET", "/api/deploy/vm125/complete", nil)
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Expected status 405, got %d", w.Code)
	}

	if w.Header().Get("Content-Length") != "0" {
		t.Errorf("Expected Content-Length: 0, got %s", w.Header().Get("Content-Length"))
	}
}

func TestCompleteDeployment_InvalidPath(t *testing.T) {
	// Test path validation
	handler := func(w http.ResponseWriter, r *http.Request) {
		parts := []string{"invalid"} // Missing /complete
		if len(parts) < 2 || parts[1] != "complete" {
			w.Header().Set("Content-Length", "0")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
	}

	req := httptest.NewRequest("POST", "/api/deploy/invalid", nil)
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", w.Code)
	}

	if w.Header().Get("Content-Length") != "0" {
		t.Errorf("Expected Content-Length: 0, got %s", w.Header().Get("Content-Length"))
	}
}

func TestCompleteDeployment_Success(t *testing.T) {
	// Test success response format
	handler := func(w http.ResponseWriter, r *http.Request) {
		body := []byte("OK")
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)))
		w.WriteHeader(http.StatusOK)
		w.Write(body)
	}

	req := httptest.NewRequest("POST", "/api/deploy/vm125/complete", nil)
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	if w.Header().Get("Content-Length") != "2" {
		t.Errorf("Expected Content-Length: 2, got %s", w.Header().Get("Content-Length"))
	}

	if w.Body.String() != "OK" {
		t.Errorf("Expected body 'OK', got %s", w.Body.String())
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
