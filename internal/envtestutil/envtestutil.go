package envtestutil

import (
	"os"
	"path/filepath"
)

// GetFirstFoundBinaryDir locates the first binary directory under
// basePath/bin/k8s. ENVTEST-based tests depend on specific binaries,
// usually located in paths set by controller-runtime. When running
// tests directly (e.g., via an IDE) without using Makefile targets,
// the BinaryAssetsDirectory must be explicitly configured.
//
// This function streamlines the process by finding the required
// binaries, similar to setting the KUBEBUILDER_ASSETS environment
// variable. To ensure the binaries are properly set up, run
// 'make setup-envtest' beforehand.
func GetFirstFoundBinaryDir(basePath string) string {
	dir := filepath.Join(basePath, "bin", "k8s")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if entry.IsDir() {
			return filepath.Join(dir, entry.Name())
		}
	}
	return ""
}
