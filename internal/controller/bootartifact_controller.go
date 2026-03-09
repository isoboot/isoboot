/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	isobootgithubiov1alpha1 "github.com/isoboot/isoboot/api/v1alpha1"
)

// BootArtifactReconciler reconciles a BootArtifact object
type BootArtifactReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	DataDir    string
	HTTPClient *http.Client
	Recorder   events.EventRecorder
}

// +kubebuilder:rbac:groups=isoboot.github.io,resources=bootartifacts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=isoboot.github.io,resources=bootartifacts/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=isoboot.github.io,resources=bootartifacts/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

func (r *BootArtifactReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var artifact isobootgithubiov1alpha1.BootArtifact
	if err := r.Get(ctx, req.NamespacedName, &artifact); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	filePath := r.filePath(&artifact)

	// Check if file exists on disk and verify hash
	if _, err := os.Stat(filePath); err == nil {
		ok, err := r.verifyExisting(ctx, &artifact, filePath)
		if err != nil {
			return ctrl.Result{}, err
		}
		if ok {
			return ctrl.Result{}, nil
		}
		// Hash mismatch — file was removed, fall through to download
	} else if !os.IsNotExist(err) {
		return r.setFailure(ctx, &artifact, fmt.Sprintf("stat file: %v", err))
	}

	return r.download(ctx, &artifact, filePath)
}

// verifyExisting checks the hash of an existing file. Returns (true, nil) if
// the file is valid, or (false, nil) if the hash mismatched and the file was
// removed (caller should proceed to download).
func (r *BootArtifactReconciler) verifyExisting(ctx context.Context, artifact *isobootgithubiov1alpha1.BootArtifact, filePath string) (bool, error) {
	log := logf.FromContext(ctx)

	computedHash, err := hashFile(filePath, artifact.Spec.SHA256 != nil)
	if err != nil {
		if os.IsNotExist(err) {
			// File deleted between Stat and Open, fall through to download
			return false, nil
		}
		return false, fmt.Errorf("hashing existing file: %w", err)
	}

	expectedHash := expectedHash(artifact)
	if !strings.EqualFold(computedHash, expectedHash) {
		log.Info("Hash mismatch for existing file, removing", "expected", expectedHash, "got", computedHash)
		if r.Recorder != nil {
			r.Recorder.Eventf(artifact, nil, "Warning", "HashMismatch", "VerifyExisting",
				"Existing file hash mismatch: expected %s got %s, re-downloading", expectedHash, computedHash)
		}
		if err := os.Remove(filePath); err != nil {
			log.Info("Could not remove mismatched file, will overwrite via download", "path", filePath, "error", err)
		}
		return false, nil
	}

	// Skip status write if already Ready — avoids a no-op update on
	// every controller restart for stable artifacts.
	if artifact.Status.Phase != isobootgithubiov1alpha1.BootArtifactPhaseReady {
		if err := r.setReady(ctx, artifact); err != nil {
			return false, err
		}
	}

	return true, nil
}

func (r *BootArtifactReconciler) download(ctx context.Context, artifact *isobootgithubiov1alpha1.BootArtifact, filePath string) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Set phase to Downloading
	artifact.Status.Phase = isobootgithubiov1alpha1.BootArtifactPhaseDownloading
	artifact.Status.Message = "Downloading"
	if err := r.Status().Update(ctx, artifact); err != nil {
		return ctrl.Result{}, fmt.Errorf("updating status: %w", err)
	}

	// Re-fetch after status update to avoid conflict
	if err := r.Get(ctx, client.ObjectKeyFromObject(artifact), artifact); err != nil {
		return ctrl.Result{}, fmt.Errorf("re-fetching artifact: %w", err)
	}

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		return r.setFailure(ctx, artifact, fmt.Sprintf("creating directory: %v", err))
	}

	tmpPath := filePath + ".tmp"
	defer func() {
		if err := os.Remove(tmpPath); err != nil && !os.IsNotExist(err) {
			log.Error(err, "Failed to clean up temp file", "path", tmpPath)
		}
	}()

	log.Info("Downloading artifact", "url", artifact.Spec.URL)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, artifact.Spec.URL, nil)
	if err != nil {
		return r.setFailure(ctx, artifact, fmt.Sprintf("creating request: %v", err))
	}

	resp, err := r.HTTPClient.Do(req)
	if err != nil {
		return r.setFailure(ctx, artifact, fmt.Sprintf("download failed: %v", err))
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		// Drain body (up to 1 MB) so the connection can be reused for keep-alive.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		return r.setFailure(ctx, artifact, fmt.Sprintf("download failed: HTTP %d", resp.StatusCode))
	}

	// Create temp file and hash while downloading
	tmpFile, err := os.Create(tmpPath)
	if err != nil {
		return r.setFailure(ctx, artifact, fmt.Sprintf("creating temp file: %v", err))
	}
	useSHA256 := artifact.Spec.SHA256 != nil
	var h hash.Hash
	if useSHA256 {
		h = sha256.New()
	} else {
		h = sha512.New()
	}

	written, err := io.Copy(tmpFile, io.TeeReader(resp.Body, h))
	if err != nil {
		_ = tmpFile.Close()
		return r.setFailure(ctx, artifact, fmt.Sprintf("writing file: %v", err))
	}
	if resp.ContentLength >= 0 && written != resp.ContentLength {
		_ = tmpFile.Close()
		return r.setFailure(ctx, artifact, fmt.Sprintf("Content-Length mismatch: expected %d bytes, got %d", resp.ContentLength, written))
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return r.setFailure(ctx, artifact, fmt.Sprintf("syncing temp file: %v", err))
	}
	if err := tmpFile.Close(); err != nil {
		return r.setFailure(ctx, artifact, fmt.Sprintf("closing temp file: %v", err))
	}

	computedHash := hex.EncodeToString(h.Sum(nil))
	expectedHash := expectedHash(artifact)

	if !strings.EqualFold(computedHash, expectedHash) {
		log.Info("Hash mismatch after download", "expected", expectedHash, "got", computedHash)
		return r.setFailure(ctx, artifact, fmt.Sprintf("hash mismatch: expected %s got %s", expectedHash, computedHash))
	}

	// Atomic rename + directory fsync for crash durability
	if err := os.Rename(tmpPath, filePath); err != nil {
		return r.setFailure(ctx, artifact, fmt.Sprintf("renaming file: %v", err))
	}
	if err := syncDir(filepath.Dir(filePath)); err != nil {
		return r.setFailure(ctx, artifact, fmt.Sprintf("syncing directory: %v", err))
	}

	if err := r.setReady(ctx, artifact); err != nil {
		return ctrl.Result{}, err
	}

	log.Info("Artifact downloaded and verified", "path", filePath)
	return ctrl.Result{}, nil
}

func (r *BootArtifactReconciler) setReady(ctx context.Context, artifact *isobootgithubiov1alpha1.BootArtifact) error {
	now := metav1.Now()
	artifact.Status.Phase = isobootgithubiov1alpha1.BootArtifactPhaseReady
	artifact.Status.Message = ""
	artifact.Status.FailureCount = 0
	artifact.Status.LastFailureTime = nil
	artifact.Status.LastChecked = &now
	if err := r.Status().Update(ctx, artifact); err != nil {
		return fmt.Errorf("updating status: %w", err)
	}
	return nil
}

func (r *BootArtifactReconciler) setFailure(ctx context.Context, artifact *isobootgithubiov1alpha1.BootArtifact, message string) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	log.Info("Artifact failure", "message", message)

	now := metav1.Now()
	artifact.Status.Phase = isobootgithubiov1alpha1.BootArtifactPhaseError
	artifact.Status.Message = message
	artifact.Status.FailureCount++
	artifact.Status.LastFailureTime = &now
	if err := r.Status().Update(ctx, artifact); err != nil {
		return ctrl.Result{}, fmt.Errorf("updating status: %w", err)
	}

	// Exponential backoff: 10s, 20s, 40s, ... capped at ~5-6 minutes
	backoff := time.Duration(1<<min(artifact.Status.FailureCount, 6)) * 5 * time.Second
	return ctrl.Result{RequeueAfter: backoff}, nil
}

func (r *BootArtifactReconciler) filePath(artifact *isobootgithubiov1alpha1.BootArtifact) string {
	filename := filenameFromURL(artifact.Spec.URL)
	return filepath.Join(r.DataDir, "artifacts", artifact.Name, filename)
}

func filenameFromURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "artifact"
	}
	name := filepath.Base(u.Path)
	if name == "" || name == "." || name == "/" || name == ".." {
		return "artifact"
	}
	return name
}

func expectedHash(artifact *isobootgithubiov1alpha1.BootArtifact) string {
	if artifact.Spec.SHA256 != nil {
		return *artifact.Spec.SHA256
	}
	if artifact.Spec.SHA512 != nil {
		return *artifact.Spec.SHA512
	}
	return ""
}

func syncDir(path string) error {
	d, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	return d.Sync()
}

func hashFile(path string, useSHA256 bool) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()

	var h hash.Hash
	if useSHA256 {
		h = sha256.New()
	} else {
		h = sha512.New()
	}

	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *BootArtifactReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if r.HTTPClient == nil {
		r.HTTPClient = &http.Client{Timeout: 30 * time.Minute}
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&isobootgithubiov1alpha1.BootArtifact{}).
		Named("bootartifact").
		Complete(r)
}
