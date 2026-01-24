package handlers

import (
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/isoboot/isoboot/internal/controllerclient"
)

type DynamicHandler struct {
	host       string
	port       string
	proxyPort  string
	ctrlClient *controllerclient.Client
}

func NewDynamicHandler(host, port, proxyPort string, ctrlClient *controllerclient.Client) *DynamicHandler {
	return &DynamicHandler{
		host:       host,
		port:       port,
		proxyPort:  proxyPort,
		ctrlClient: ctrlClient,
	}
}

// ServePreseed serves preseed configuration files
// Path format: /dynamic/{hostname}/{target}/preseed.cfg
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

	// Parse path: /api/deploy/{hostname}/complete
	path := strings.TrimPrefix(r.URL.Path, "/api/deploy/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 || parts[1] != "complete" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	hostname := parts[0]
	ctx := r.Context()

	// Mark deploy as completed via controller (using hostname)
	if err := h.ctrlClient.MarkBootCompleted(ctx, hostname); err != nil {
		log.Printf("Error completing deploy for %s: %v", hostname, err)
		w.WriteHeader(http.StatusNotFound)
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
