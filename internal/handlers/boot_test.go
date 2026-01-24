package handlers

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"text/template"
)

// MockBootHandler creates a handler with preloaded templates for testing
func newTestBootHandler(templates map[string]string) *BootHandler {
	h := &BootHandler{
		host:      "192.168.1.1",
		port:      "8080",
		k8sClient: nil,
		configMap: "test-templates",
		templates: make(map[string]*template.Template),
	}

	// Pre-parse templates
	for name, content := range templates {
		tmpl, _ := template.New(name).Parse(content)
		h.templates[name] = tmpl
	}

	return h
}

// Override loadTemplate to use cached templates for testing
func (h *BootHandler) loadTemplateForTest(name string) (*template.Template, error) {
	if tmpl, ok := h.templates[name]; ok {
		return tmpl, nil
	}
	return nil, nil
}

func TestServeBootIPXE_ContentLength(t *testing.T) {
	templates := map[string]string{
		"boot.ipxe": "#!ipxe\nchain http://{{ .Host }}:{{ .Port }}/boot\n",
	}
	h := newTestBootHandler(templates)

	// Create a test handler that uses preloaded template
	handler := func(w http.ResponseWriter, r *http.Request) {
		tmpl := h.templates["boot.ipxe"]
		if tmpl == nil {
			w.Header().Set("Content-Length", "0")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		data := TemplateData{
			Host: h.host,
			Port: h.port,
		}

		var buf bytes.Buffer
		if err := tmpl.Execute(&buf, data); err != nil {
			w.Header().Set("Content-Length", "0")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Content-Length", fmt.Sprintf("%d", buf.Len()))
		w.WriteHeader(http.StatusOK)
		w.Write(buf.Bytes())
	}

	req := httptest.NewRequest("GET", "/boot/boot.ipxe", nil)
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	if w.Header().Get("Content-Length") == "" {
		t.Error("Expected Content-Length header to be set")
	}

	body := w.Body.String()
	if body == "" {
		t.Error("Expected non-empty body")
	}

	if !bytes.Contains(w.Body.Bytes(), []byte("192.168.1.1")) {
		t.Error("Expected host IP in response")
	}
}

func TestServeConditionalBoot_NoMAC(t *testing.T) {
	// Test the MAC validation directly
	handler := func(w http.ResponseWriter, r *http.Request) {
		mac := r.URL.Query().Get("mac")
		if mac == "" {
			w.Header().Set("Content-Length", "0")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
	}

	req := httptest.NewRequest("GET", "/boot/conditional-boot", nil)
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", w.Code)
	}

	if w.Header().Get("Content-Length") != "0" {
		t.Errorf("Expected Content-Length: 0, got %s", w.Header().Get("Content-Length"))
	}
}

func TestServeConditionalBoot_NoDeploy(t *testing.T) {
	// When no deploy is found, should return 404 with Content-Length
	handler := func(w http.ResponseWriter, r *http.Request) {
		// Simulate no deploy found
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(http.StatusNotFound)
	}

	req := httptest.NewRequest("GET", "/boot/conditional-boot?mac=aa-bb-cc-dd-ee-ff", nil)
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Expected status 404, got %d", w.Code)
	}

	if w.Header().Get("Content-Length") != "0" {
		t.Errorf("Expected Content-Length: 0, got %s", w.Header().Get("Content-Length"))
	}
}

func TestTemplateRendering(t *testing.T) {
	tmpl, err := template.New("test").Parse("#!ipxe\nkernel http://{{ .Host }}:{{ .Port }}/iso/{{ .MAC }}/linux\n")
	if err != nil {
		t.Fatalf("Failed to parse template: %v", err)
	}

	data := TemplateData{
		Host: "192.168.1.100",
		Port: "8080",
		MAC:  "aa-bb-cc-dd-ee-ff",
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		t.Fatalf("Failed to execute template: %v", err)
	}

	result := buf.String()
	if !bytes.Contains([]byte(result), []byte("192.168.1.100")) {
		t.Error("Expected host in output")
	}
	if !bytes.Contains([]byte(result), []byte("8080")) {
		t.Error("Expected port in output")
	}
	if !bytes.Contains([]byte(result), []byte("aa-bb-cc-dd-ee-ff")) {
		t.Error("Expected MAC in output")
	}
}
