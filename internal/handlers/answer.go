package handlers

import (
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/isoboot/isoboot/internal/controllerclient"
)

type AnswerHandler struct {
	host       string
	port       string
	proxyPort  string
	ctrlClient *controllerclient.Client
}

func NewAnswerHandler(host, port, proxyPort string, ctrlClient *controllerclient.Client) *AnswerHandler {
	return &AnswerHandler{
		host:       host,
		port:       port,
		proxyPort:  proxyPort,
		ctrlClient: ctrlClient,
	}
}

// ServeAnswer serves response template files (preseed/kickstart/autoinstall)
// Path format: /answer/{provisionName}/{filename}
func (h *AnswerHandler) ServeAnswer(w http.ResponseWriter, r *http.Request) {
	// Parse path: /answer/{provisionName}/{filename}
	path := strings.TrimPrefix(r.URL.Path, "/answer/")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) < 2 {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	provisionName := parts[0]
	filename := parts[1]
	ctx := r.Context()

	// Get rendered template from controller
	content, err := h.ctrlClient.GetRenderedTemplate(ctx, provisionName, filename)
	if err != nil {
		log.Printf("Error getting rendered template for %s/%s: %v", provisionName, filename, err)
		// Distinguish between "not found" (404) and server/transport errors (502)
		if strings.Contains(err.Error(), "grpc call:") {
			w.WriteHeader(http.StatusBadGateway)
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
		return
	}

	w.Header().Set("Content-Type", "text/plain")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(content)))
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(content))
}

// RegisterRoutes registers answer file routes
func (h *AnswerHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/answer/", h.ServeAnswer)
}
