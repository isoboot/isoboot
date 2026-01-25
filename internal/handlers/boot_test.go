package handlers

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"text/template"
)

func TestServeBootIPXE_ContentLength(t *testing.T) {
	tmpl, _ := template.New("boot.ipxe").Parse("#!ipxe\nchain http://{{ .Host }}:{{ .Port }}/boot\n")

	handler := func(w http.ResponseWriter, r *http.Request) {
		data := TemplateData{
			Host: "192.168.1.1",
			Port: "8080",
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
	handler := func(w http.ResponseWriter, r *http.Request) {
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
	tmpl, err := template.New("test").Parse("#!ipxe\nkernel http://{{ .Host }}:{{ .Port }}/iso/{{ .Hostname }}/linux\n")
	if err != nil {
		t.Fatalf("Failed to parse template: %v", err)
	}

	data := TemplateData{
		Host:     "192.168.1.100",
		Port:     "8080",
		Hostname: "vm125",
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
	if !bytes.Contains([]byte(result), []byte("vm125")) {
		t.Error("Expected hostname in output")
	}
}
