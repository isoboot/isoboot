package handlers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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

	h := NewAnswerHandler("10.0.0.1", "3128", mock)
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
	h := NewAnswerHandler("10.0.0.1", "3128", &mockAnswerClient{})
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

	h := NewAnswerHandler("10.0.0.1", "3128", mock)
	req := httptest.NewRequest("GET", "/answer/missing-prov/preseed.cfg", nil)
	w := httptest.NewRecorder()

	h.ServeAnswer(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestServeAnswer_GRPCError(t *testing.T) {
	mock := &mockAnswerClient{
		getProvision: func(ctx context.Context, name string) (*controllerclient.ProvisionInfo, error) {
			return nil, fmt.Errorf("grpc call: connection refused")
		},
	}

	h := NewAnswerHandler("10.0.0.1", "3128", mock)
	req := httptest.NewRequest("GET", "/answer/prov-1/preseed.cfg", nil)
	w := httptest.NewRecorder()

	h.ServeAnswer(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", w.Code)
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

	h := NewAnswerHandler("10.0.0.1", "3128", mock)
	req := httptest.NewRequest("GET", "/answer/prov-1/nonexistent.cfg", nil)
	w := httptest.NewRecorder()

	h.ServeAnswer(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}
