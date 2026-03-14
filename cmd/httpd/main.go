package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"syscall"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	isobootgithubiov1alpha1 "github.com/isoboot/isoboot/api/v1alpha1"
	"github.com/isoboot/isoboot/internal/controller"
	"github.com/isoboot/isoboot/internal/httpd"
)

var macRegexp = regexp.MustCompile(`^([0-9a-fA-F]{2}-){5}[0-9a-fA-F]{2}$`)

type bootDirectiveFunc func(ctx context.Context, mac string) (*httpd.BootDirective, error)

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

	handler := conditionalBootHandler(func(reqCtx context.Context, mac string) (*httpd.BootDirective, error) {
		return httpd.BootDirectiveForMAC(reqCtx, c, ns, mac)
	})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /conditional-boot", handler)
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

func conditionalBootHandler(getDirective bootDirectiveFunc) http.HandlerFunc {
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

func healthzHandler(w http.ResponseWriter, _ *http.Request) {
	body := "OK"
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprint(w, body)
}
