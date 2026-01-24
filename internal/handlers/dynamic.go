package handlers

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/isoboot/isoboot/internal/k8s"
)

type DynamicHandler struct {
	host      string
	port      string
	proxyPort string
	k8sClient *k8s.Client
}

func NewDynamicHandler(host, port, proxyPort string, k8sClient *k8s.Client) *DynamicHandler {
	return &DynamicHandler{
		host:      host,
		port:      port,
		proxyPort: proxyPort,
		k8sClient: k8sClient,
	}
}

// ServePreseed serves preseed configuration files
// Path format: /dynamic/{mac}/{target}/preseed.cfg
// Returns 200 with Content-Length: 0 (no content yet)
func (h *DynamicHandler) ServePreseed(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	w.Header().Set("Content-Length", "0")
	w.WriteHeader(http.StatusOK)
}

// CompleteDeployment marks a deployment as completed
func (h *DynamicHandler) CompleteDeployment(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// Parse path: /api/deploy/{mac}/complete
	path := strings.TrimPrefix(r.URL.Path, "/api/deploy/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 || parts[1] != "complete" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	mac := strings.ToLower(parts[0])

	deploy, err := h.k8sClient.FindDeployByMAC(context.Background(), mac)
	if err != nil || deploy == nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	if err := h.k8sClient.UpdateDeployStatus(context.Background(), deploy.Name, "Completed", "Installation completed"); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	body := []byte("OK")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)))
	w.WriteHeader(http.StatusOK)
	w.Write(body)
}

// RegisterRoutes registers dynamic content routes
func (h *DynamicHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/dynamic/", h.ServePreseed)
	mux.HandleFunc("/api/deploy/", h.CompleteDeployment)
}
