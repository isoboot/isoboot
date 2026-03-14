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
