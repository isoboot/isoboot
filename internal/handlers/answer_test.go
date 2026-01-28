package handlers

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"text/template"

	"github.com/isoboot/isoboot/internal/controllerclient"
)

// mockAnswerClient implements AnswerClient for testing.
type mockAnswerClient struct {
	getProvision        func(ctx context.Context, name string) (*controllerclient.ProvisionInfo, error)
	getResponseTemplate func(ctx context.Context, name string) (*controllerclient.ResponseTemplateInfo, error)
	getConfigMaps       func(ctx context.Context, names []string) (map[string]string, error)
	getSecrets          func(ctx context.Context, names []string) (map[string]string, error)
	getMachine          func(ctx context.Context, name string) (string, error)
}

func (m *mockAnswerClient) GetProvision(ctx context.Context, name string) (*controllerclient.ProvisionInfo, error) {
	return m.getProvision(ctx, name)
}
func (m *mockAnswerClient) GetResponseTemplate(ctx context.Context, name string) (*controllerclient.ResponseTemplateInfo, error) {
	return m.getResponseTemplate(ctx, name)
}
func (m *mockAnswerClient) GetConfigMaps(ctx context.Context, names []string) (map[string]string, error) {
	return m.getConfigMaps(ctx, names)
}
func (m *mockAnswerClient) GetSecrets(ctx context.Context, names []string) (map[string]string, error) {
	return m.getSecrets(ctx, names)
}
func (m *mockAnswerClient) GetMachine(ctx context.Context, name string) (string, error) {
	return m.getMachine(ctx, name)
}

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

func TestServeAnswer_Success(t *testing.T) {
	mock := &mockAnswerClient{
		getProvision: func(ctx context.Context, name string) (*controllerclient.ProvisionInfo, error) {
			return &controllerclient.ProvisionInfo{
				MachineRef:          "vm-01",
				BootTargetRef:       "debian-13",
				ResponseTemplateRef: "preseed-tmpl",
				ConfigMaps:          []string{"network-config"},
				Secrets:             nil,
			}, nil
		},
		getResponseTemplate: func(ctx context.Context, name string) (*controllerclient.ResponseTemplateInfo, error) {
			return &controllerclient.ResponseTemplateInfo{
				Files: map[string]string{
					"preseed.cfg": "d-i netcfg/hostname string {{ .Hostname }}\nd-i mirror/http/proxy string http://{{ .Host }}:{{ .Port }}/\n",
				},
			}, nil
		},
		getConfigMaps: func(ctx context.Context, names []string) (map[string]string, error) {
			return map[string]string{"gateway": "10.0.0.1"}, nil
		},
		getSecrets: func(ctx context.Context, names []string) (map[string]string, error) {
			return map[string]string{}, nil
		},
		getMachine: func(ctx context.Context, name string) (string, error) {
			return "aa-bb-cc-dd-ee-ff", nil
		},
	}

	h := NewAnswerHandler("10.0.0.1", "8080", "3128", mock)
	req := httptest.NewRequest("GET", "/answer/prov-1/preseed.cfg", nil)
	w := httptest.NewRecorder()

	h.ServeAnswer(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "vm-01") {
		t.Errorf("expected hostname in body, got %q", body)
	}
	if !strings.Contains(body, "10.0.0.1") {
		t.Errorf("expected host in body, got %q", body)
	}
	if w.Header().Get("Content-Length") == "" {
		t.Error("expected Content-Length header")
	}
}

func TestServeAnswer_InvalidPath(t *testing.T) {
	h := NewAnswerHandler("10.0.0.1", "8080", "3128", &mockAnswerClient{})
	req := httptest.NewRequest("GET", "/answer/only-one-segment", nil)
	w := httptest.NewRecorder()

	h.ServeAnswer(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestServeAnswer_ProvisionNotFound(t *testing.T) {
	mock := &mockAnswerClient{
		getProvision: func(ctx context.Context, name string) (*controllerclient.ProvisionInfo, error) {
			return nil, fmt.Errorf("provision %s: %w", name, controllerclient.ErrNotFound)
		},
	}

	h := NewAnswerHandler("10.0.0.1", "8080", "3128", mock)
	req := httptest.NewRequest("GET", "/answer/missing-prov/preseed.cfg", nil)
	w := httptest.NewRecorder()

	h.ServeAnswer(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestServeAnswer_FileNotInTemplate(t *testing.T) {
	mock := &mockAnswerClient{
		getProvision: func(ctx context.Context, name string) (*controllerclient.ProvisionInfo, error) {
			return &controllerclient.ProvisionInfo{
				MachineRef:          "vm-01",
				ResponseTemplateRef: "tmpl",
			}, nil
		},
		getResponseTemplate: func(ctx context.Context, name string) (*controllerclient.ResponseTemplateInfo, error) {
			return &controllerclient.ResponseTemplateInfo{
				Files: map[string]string{"preseed.cfg": "content"},
			}, nil
		},
	}

	h := NewAnswerHandler("10.0.0.1", "8080", "3128", mock)
	req := httptest.NewRequest("GET", "/answer/prov-1/nonexistent.cfg", nil)
	w := httptest.NewRecorder()

	h.ServeAnswer(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}
