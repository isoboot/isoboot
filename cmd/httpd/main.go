package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"syscall"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	isobootgithubiov1alpha1 "github.com/isoboot/isoboot/api/v1alpha1"
	"github.com/isoboot/isoboot/internal/controller"
	"github.com/isoboot/isoboot/internal/httpd"
)

var macRegexp = regexp.MustCompile(`^([0-9a-fA-F]{2}-){5}[0-9a-fA-F]{2}$`)

type bootDirectiveFunc func(ctx context.Context, mac string) (*httpd.BootDirective, error)
type renderAutomationFunc func(ctx context.Context, provisionName, fileName, statusURL string) (string, error)
type updatePhaseFunc func(
	ctx context.Context, provisionName string,
	phase isobootgithubiov1alpha1.ProvisionPhase, message string,
) error

func main() {
	listenAddr := flag.String("listen-addr", ":8080", "address to listen on")
	namespace := flag.String("namespace", "default", "namespace to query")
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))

	sch := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(sch))
	utilruntime.Must(isobootgithubiov1alpha1.AddToScheme(sch))

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:  sch,
		Metrics: metricsserver.Options{BindAddress: "0"},
		Client: client.Options{
			Cache: &client.CacheOptions{
				DisableFor: []client.Object{
					&isobootgithubiov1alpha1.BootArtifact{},
					&isobootgithubiov1alpha1.BootConfig{},
					&isobootgithubiov1alpha1.ProvisionAutomation{},
					&corev1.ConfigMap{},
					&corev1.Secret{},
				},
			},
		},
	})
	if err != nil {
		slog.Error("failed to create manager", "error", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := controller.SetupIndexers(ctx, mgr); err != nil {
		slog.Error("failed to set up indexers", "error", err)
		os.Exit(1)
	}

	mgrErrCh := make(chan error, 1)
	go func() {
		if err := mgr.Start(ctx); err != nil {
			mgrErrCh <- err
		}
	}()

	if !mgr.GetCache().WaitForCacheSync(ctx) {
		slog.Error("cache sync failed")
		os.Exit(1)
	}

	c := mgr.GetClient()
	ns := *namespace
	proxyPort := os.Getenv("PROXY_PORT")

	handler := conditionalBootHandler(func(reqCtx context.Context, mac string) (*httpd.BootDirective, error) {
		return httpd.BootDirectiveForMAC(reqCtx, c, ns, mac)
	}, proxyPort)

	renderFile := func(
		reqCtx context.Context, provisionName, fileName, statusURL string,
	) (string, error) {
		return httpd.RenderAutomationFile(reqCtx, c, ns, provisionName, fileName, statusURL)
	}
	automationHandler := automationFileHandler(renderFile)

	updatePhase := func(
		reqCtx context.Context, provisionName string,
		phase isobootgithubiov1alpha1.ProvisionPhase, message string,
	) error {
		return httpd.UpdateProvisionPhase(reqCtx, c, ns, provisionName, phase, message)
	}
	statusHandler := updateStatusHandler(updatePhase)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /conditional-boot", handler)
	mux.HandleFunc("GET /automation/{provisionName}/{fileName}", automationHandler)
	mux.HandleFunc("POST /status", statusHandler)
	mux.HandleFunc("GET /status", statusHandler)
	mux.HandleFunc("GET /healthz", healthzHandler)

	srv := &http.Server{
		Addr:         *listenAddr,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	srvErrCh := make(chan error, 1)
	go func() {
		slog.Info("starting server", "addr", *listenAddr)
		if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			srvErrCh <- err
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-mgrErrCh:
		slog.Error("manager error", "error", err)
		os.Exit(1)
	case err := <-srvErrCh:
		slog.Error("server error", "error", err)
		os.Exit(1)
	case <-quit:
	}

	slog.Info("shutting down server")
	shutCtx, shutCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer shutCancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		slog.Error("shutdown error", "error", err)
	}
}

func conditionalBootHandler(
	getDirective bootDirectiveFunc, proxyPort string,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		mac := r.URL.Query().Get("mac")
		if mac == "" {
			http.Error(w, "missing required parameter: mac", http.StatusBadRequest)
			return
		}
		if !macRegexp.MatchString(mac) {
			http.Error(w, "invalid mac address format", http.StatusBadRequest)
			return
		}

		directive, err := getDirective(r.Context(), mac)
		if err != nil {
			if httpd.IsDuplicateError(err) {
				slog.Error("duplicate match", "mac", mac, "error", err)
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			slog.Error("boot directive lookup failed", "mac", mac, "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		if directive == nil {
			http.Error(w, "no pending provision for MAC", http.StatusNotFound)
			return
		}

		slog.Info("conditional-boot request", "mac", mac)

		if directive.KernelArgs != "" {
			host := resolveHost(r)
			nodeIP := host
			if h, _, err := net.SplitHostPort(nodeIP); err == nil {
				nodeIP = h
			}
			baseURL := fmt.Sprintf("http://%s/dynamic/automation/%s",
				host, directive.ProvisionName)
			statusURL := fmt.Sprintf("http://%s/dynamic/status", host)
			proxyURL := ""
			if proxyPort != "" {
				proxyURL = fmt.Sprintf("http://%s:%s", nodeIP, proxyPort)
			}
			rendered, err := httpd.RenderKernelArgs(
				directive.KernelArgs, httpd.KernelArgsData{
					ProvisionAutomationBaseURL: baseURL,
					ProxyURL:                   proxyURL,
					UpdatePhaseURL:             statusURL,
					ProvisionName:              directive.ProvisionName,
				})
			if err != nil {
				slog.Error("kernel args template failed",
					"mac", mac, "error", err)
				http.Error(w, "internal error",
					http.StatusInternalServerError)
				return
			}
			directive.KernelArgs = rendered
		}

		kernelLine := fmt.Sprintf("kernel /static/%s", directive.KernelPath)
		if directive.KernelArgs != "" {
			kernelLine += " " + directive.KernelArgs
		}
		body := fmt.Sprintf("#!ipxe\n%s\ninitrd /static/%s\nboot\n",
			kernelLine, directive.InitrdPath)

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, body)
	}
}

// resolveHost returns the effective host:port from the request,
// honoring X-Forwarded-Host and X-Forwarded-Port headers.
func resolveHost(r *http.Request) string {
	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}
	if port := r.Header.Get("X-Forwarded-Port"); port != "" {
		h, _, _ := net.SplitHostPort(host)
		if h != "" {
			host = h
		}
		host = net.JoinHostPort(host, port)
	}
	return host
}

var nameRegexp = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$`)

func automationFileHandler(render renderAutomationFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		provisionName := r.PathValue("provisionName")
		fileName := r.PathValue("fileName")

		if !nameRegexp.MatchString(provisionName) || len(provisionName) > 253 {
			http.Error(w, "invalid provision name", http.StatusBadRequest)
			return
		}
		if fileName == "" {
			http.Error(w, "missing file name", http.StatusBadRequest)
			return
		}

		statusURL := fmt.Sprintf(
			"http://%s/dynamic/status", resolveHost(r))

		body, err := render(r.Context(), provisionName, fileName, statusURL)
		if err != nil {
			if httpd.IsAutomationNotFound(err) {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			slog.Error("automation render failed",
				"provision", provisionName, "file", fileName, "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, body)
	}
}

// allowedPhases maps phase parameter values to ProvisionPhase constants
// and their default status messages.
var allowedPhases = map[string]struct {
	phase   isobootgithubiov1alpha1.ProvisionPhase
	message string
}{
	"InProgress": {
		isobootgithubiov1alpha1.ProvisionPhaseInProgress,
		"Installation in progress",
	},
	"Complete": {
		isobootgithubiov1alpha1.ProvisionPhaseComplete,
		"Installation complete",
	},
}

func updateStatusHandler(update updatePhaseFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		provisionName := r.FormValue("provisionName")
		if provisionName == "" {
			http.Error(w,
				"missing required parameter: provisionName",
				http.StatusBadRequest)
			return
		}
		if !nameRegexp.MatchString(provisionName) ||
			len(provisionName) > 253 {
			http.Error(w, "invalid provision name",
				http.StatusBadRequest)
			return
		}

		phaseParam := r.FormValue("phase")
		entry, ok := allowedPhases[phaseParam]
		if !ok {
			http.Error(w, "invalid phase: must be InProgress or Complete",
				http.StatusBadRequest)
			return
		}

		err := update(r.Context(), provisionName, entry.phase, entry.message)
		if err != nil {
			if httpd.IsProvisionNotFound(err) {
				http.Error(w, "provision not found",
					http.StatusNotFound)
				return
			}
			if httpd.IsProvisionPhaseError(err) {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			slog.Error("update provision status failed",
				"provision", provisionName,
				"phase", phaseParam, "error", err)
			http.Error(w, "internal error",
				http.StatusInternalServerError)
			return
		}

		slog.Info("provision status updated",
			"provision", provisionName, "phase", phaseParam)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "OK")
	}
}

func healthzHandler(w http.ResponseWriter, _ *http.Request) {
	body := "OK"
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprint(w, body)
}
