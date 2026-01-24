package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/isoboot/isoboot/internal/config"
	"github.com/isoboot/isoboot/internal/handlers"
	"github.com/isoboot/isoboot/internal/k8s"
)

func main() {
	var (
		host              string
		port              string
		proxyPort         string
		namespace         string
		templatesConfigMap string
		isoPath           string
		configPath        string
	)

	flag.StringVar(&host, "host", "", "Host IP to advertise in boot scripts")
	flag.StringVar(&port, "port", "8080", "HTTP server port")
	flag.StringVar(&proxyPort, "proxy-port", "3128", "Squid proxy port")
	flag.StringVar(&namespace, "namespace", "", "Kubernetes namespace")
	flag.StringVar(&templatesConfigMap, "templates-configmap", "", "ConfigMap containing boot templates")
	flag.StringVar(&isoPath, "iso-path", "/opt/isoboot/iso", "Path to ISO storage directory")
	flag.StringVar(&configPath, "config", "", "Path to config file for hot-reload")
	flag.Parse()

	if host == "" {
		log.Fatal("--host is required")
	}
	if namespace == "" {
		namespace = os.Getenv("POD_NAMESPACE")
		if namespace == "" {
			log.Fatal("--namespace or POD_NAMESPACE is required")
		}
	}
	if templatesConfigMap == "" {
		log.Fatal("--templates-configmap is required")
	}

	// Initialize config watcher for hot-reload
	configWatcher, err := config.NewConfigWatcher(configPath)
	if err != nil {
		log.Fatalf("Failed to create config watcher: %v", err)
	}
	configWatcher.Start()
	defer configWatcher.Stop()

	// Initialize Kubernetes client
	k8sClient, err := k8s.NewClient(namespace)
	if err != nil {
		log.Fatalf("Failed to create Kubernetes client: %v", err)
	}

	// Set up HTTP handlers
	mux := http.NewServeMux()

	// Health check
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "OK")
	})

	// Boot handlers
	bootHandler := handlers.NewBootHandler(host, port, k8sClient, templatesConfigMap)
	bootHandler.RegisterRoutes(mux)

	// ISO content handlers
	isoHandler := handlers.NewISOHandler(isoPath, configWatcher)
	isoHandler.RegisterRoutes(mux)

	// Dynamic content handlers
	dynamicHandler := handlers.NewDynamicHandler(host, port, proxyPort, k8sClient)
	dynamicHandler.RegisterRoutes(mux)

	// Start server
	addr := fmt.Sprintf(":%s", port)
	log.Printf("Starting isoboot-http on %s:%s", host, port)
	log.Printf("ISO path: %s", isoPath)
	log.Printf("Namespace: %s", namespace)
	log.Printf("Templates ConfigMap: %s", templatesConfigMap)

	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
