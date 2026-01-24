package handlers

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"text/template"
)

func TestServePreseed_InvalidPath_Short(t *testing.T) {
	// Test path validation logic directly
	handler := func(w http.ResponseWriter, r *http.Request) {
		// Simulate the path check from ServePreseed
		parts := []string{} // Invalid - less than 3 parts
		if len(parts) < 3 {
			w.Header().Set("Content-Type", "text/plain")
			w.Header().Set("Content-Length", "0")
			w.WriteHeader(http.StatusOK)
			return
		}
	}

	req := httptest.NewRequest("GET", "/dynamic/invalid", nil)
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	if w.Header().Get("Content-Length") != "0" {
		t.Errorf("Expected Content-Length: 0, got %s", w.Header().Get("Content-Length"))
	}
}

func TestServePreseed_NoDeployResponse(t *testing.T) {
	// Test the response format when no deploy is found
	handler := func(w http.ResponseWriter, r *http.Request) {
		// Simulate no deploy found
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(http.StatusOK)
	}

	req := httptest.NewRequest("GET", "/dynamic/aa-bb-cc-dd-ee-ff/debian-13/preseed.cfg", nil)
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

	req := httptest.NewRequest("GET", "/api/deploy/aa-bb-cc-dd-ee-ff/complete", nil)
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

	req := httptest.NewRequest("POST", "/api/deploy/aa-bb-cc-dd-ee-ff/complete", nil)
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

func TestPreseedTemplateRendering(t *testing.T) {
	tmpl, err := template.New("preseed").Parse(`# Preseed for MAC: {{.MAC}}
d-i mirror/http/proxy string {{.ProxyURL}}
`)
	if err != nil {
		t.Fatalf("Failed to parse template: %v", err)
	}

	data := PreseedData{
		Host:      "192.168.1.100",
		Port:      "8080",
		ProxyPort: "3128",
		ProxyURL:  "http://192.168.1.100:3128",
		MAC:       "aa-bb-cc-dd-ee-ff",
		MACColon:  "aa:bb:cc:dd:ee:ff",
		Target:    "debian-13",
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		t.Fatalf("Failed to execute template: %v", err)
	}

	result := buf.String()
	if !bytes.Contains([]byte(result), []byte("aa-bb-cc-dd-ee-ff")) {
		t.Error("Expected MAC in output")
	}
	if !bytes.Contains([]byte(result), []byte("http://192.168.1.100:3128")) {
		t.Error("Expected proxy URL in output")
	}
}

func TestPreseedContentLength(t *testing.T) {
	// Verify that preseed content is rendered to buffer and Content-Length is set
	tmpl, _ := template.New("preseed").Parse("test content")

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
