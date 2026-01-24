package handlers

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"
	"text/template"

	"github.com/isoboot/isoboot/internal/k8s"
)

type BootHandler struct {
	host      string
	port      string
	k8sClient *k8s.Client
	configMap string
}

func NewBootHandler(host, port string, k8sClient *k8s.Client, configMap string) *BootHandler {
	return &BootHandler{
		host:      host,
		port:      port,
		k8sClient: k8sClient,
		configMap: configMap,
	}
}

// TemplateData is passed to boot templates
type TemplateData struct {
	Host string
	Port string
	MAC  string
}

func (h *BootHandler) loadTemplate(name string) (*template.Template, error) {
	cm, err := h.k8sClient.GetConfigMap(context.Background(), h.configMap)
	if err != nil {
		return nil, fmt.Errorf("get configmap: %w", err)
	}

	content, ok := cm.Data[name]
	if !ok {
		return nil, fmt.Errorf("template not found in configmap: %s", name)
	}

	return template.New(name).Parse(content)
}

// ServeBootIPXE serves the initial boot.ipxe script
func (h *BootHandler) ServeBootIPXE(w http.ResponseWriter, r *http.Request) {
	tmpl, err := h.loadTemplate("boot.ipxe")
	if err != nil {
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

// ServeConditionalBoot checks Deploy CRDs and returns appropriate boot script
// Returns 404 with Content-Length if no deploy found (iPXE falls back to local boot)
func (h *BootHandler) ServeConditionalBoot(w http.ResponseWriter, r *http.Request) {
	mac := r.URL.Query().Get("mac")
	if mac == "" {
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// MAC must be dash-separated (xx-xx-xx-xx-xx-xx from iPXE)
	mac = strings.ToLower(mac)

	// Find pending deploy for this MAC
	deploy, err := h.k8sClient.FindDeployByMAC(context.Background(), mac, "Pending")
	if err != nil {
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	if deploy == nil {
		// No pending deploy found - return 404 so iPXE falls back to local boot
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(http.StatusNotFound)
		return
	}

	// Load target template (e.g., debian-13.ipxe)
	templateName := deploy.Spec.Target + ".ipxe"
	tmpl, err := h.loadTemplate(templateName)
	if err != nil {
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	data := TemplateData{
		Host: h.host,
		Port: h.port,
		MAC:  mac, // Keep dash format for URLs
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

	// Update deploy status to InProgress
	if err := h.k8sClient.UpdateDeployStatus(context.Background(), deploy.Name, "InProgress", "Boot script served"); err != nil {
		// Log but don't fail the request
		fmt.Printf("Warning: failed to update deploy status: %v\n", err)
	}
}

// RegisterRoutes registers boot-related routes
func (h *BootHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/boot/boot.ipxe", h.ServeBootIPXE)
	mux.HandleFunc("/boot/conditional-boot", h.ServeConditionalBoot)
}
