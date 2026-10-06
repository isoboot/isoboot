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

package docs

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	"github.com/isoboot/isoboot/internal/envtestutil"
)

const isobootGroup = "isoboot.github.io"

// minimumObjects guards against a parser that silently finds nothing.
const minimumObjects = 10

// objectLikeLine finds a block that is meant to be (part of) an object even
// when it does not parse as YAML.
var objectLikeLine = regexp.MustCompile(`(?m)^(apiVersion|kind):`)

// documentationObject is one YAML document of a code block.
type documentationObject struct {
	location string // file, block and document, for messages
	object   *unstructured.Unstructured
}

// TestDocumentationObjectsAreAccepted creates every isoboot object, ConfigMap
// and Secret written in a yaml block under docs/ against the real CRDs.
func TestDocumentationObjectsAreAccepted(t *testing.T) {
	var objects []documentationObject
	for _, path := range documentationFiles(t) {
		file := readMarkdownFile(t, path)
		for _, block := range file.blocks {
			if block.language != "yaml" || block.markedToSkip {
				continue
			}
			location := fmt.Sprintf("%s yaml block %d (line %d)",
				relativeName(path), block.languageNumber, block.startLine)
			found, err := objectsInBlock(location, block.content)
			if err != nil {
				t.Error(err)
				continue
			}
			objects = append(objects, found...)
		}
	}
	if len(objects) < minimumObjects {
		t.Fatalf("found %d objects to check in docs/, expected at least %d", len(objects), minimumObjects)
	}

	apiClient := startAPIServer(t)
	ctx := context.Background()
	namespaces := map[string]string{}
	for _, entry := range objects {
		// One namespace per block: blocks on different pages reuse names.
		blockLocation := entry.location[:strings.LastIndex(entry.location, " document ")]
		namespace, ok := namespaces[blockLocation]
		if !ok {
			namespace = fmt.Sprintf("docs-%d", len(namespaces)+1)
			namespaces[blockLocation] = namespace
			scratch := &corev1.Namespace{Name: namespace}
			if err := apiClient.Create(ctx, scratch); err != nil {
				t.Fatalf("creating namespace %s: %v", namespace, err)
			}
		}
		entry.object.SetNamespace(namespace)
		err := apiClient.Create(ctx, entry.object, client.FieldValidation(metav1.FieldValidationStrict))
		if err != nil {
			t.Errorf("%s: %s %q rejected: %v",
				entry.location, entry.object.GetKind(), entry.object.GetName(), err)
		}
	}
	t.Logf("created %d objects from %d yaml blocks", len(objects), len(namespaces))
}

// objectsInBlock returns the objects of the kinds this test checks. Blocks
// that are not Kubernetes objects at all are ignored; a block that looks
// like an incomplete object is an error, unless it is marked to skip.
func objectsInBlock(location, content string) ([]documentationObject, error) {
	reader := utilyaml.NewYAMLReader(bufio.NewReader(strings.NewReader(content)))
	var objects []documentationObject
	for documentNumber := 1; ; documentNumber++ {
		document, err := reader.Read()
		if errors.Is(err, io.EOF) {
			return objects, nil
		}
		if err != nil {
			return nil, fmt.Errorf("%s: splitting YAML documents: %w", location, err)
		}
		documentLocation := fmt.Sprintf("%s document %d", location, documentNumber)
		var fields map[string]any
		if err := utilyaml.Unmarshal(document, &fields); err != nil {
			if objectLikeLine.Match(document) {
				return nil, fmt.Errorf("%s: not valid YAML: %w", documentLocation, err)
			}
			continue // a template or another text that is not an object
		}
		_, hasAPIVersion := fields["apiVersion"]
		_, hasKind := fields["kind"]
		if !hasAPIVersion || !hasKind {
			if looksLikePartialObject(fields) {
				return nil, fmt.Errorf("%s: part of an object without apiVersion and kind; "+
					"complete it or mark the block with %q", documentLocation, skipMarker+" (why) -->")
			}
			continue
		}
		object := &unstructured.Unstructured{Object: fields}
		if !checkedKind(object) {
			continue
		}
		objects = append(objects, documentationObject{location: documentLocation, object: object})
	}
}

func looksLikePartialObject(fields map[string]any) bool {
	for _, key := range []string{"apiVersion", "kind", "metadata", "spec"} {
		if _, ok := fields[key]; ok {
			return true
		}
	}
	return false
}

func checkedKind(object *unstructured.Unstructured) bool {
	kind := object.GroupVersionKind()
	if kind.Group == isobootGroup {
		return true
	}
	return kind.Group == "" && kind.Version == "v1" && (kind.Kind == "ConfigMap" || kind.Kind == "Secret")
}

// startAPIServer starts envtest with the generated CRDs and returns a client.
func startAPIServer(t *testing.T) client.Client {
	t.Helper()
	environment := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join(repositoryRoot, "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}
	if directory := envtestutil.GetFirstFoundBinaryDir(repositoryRoot); directory != "" {
		environment.BinaryAssetsDirectory = directory
	}
	config, err := environment.Start()
	if err != nil {
		t.Fatalf("starting envtest: %v", err)
	}
	t.Cleanup(func() {
		if err := environment.Stop(); err != nil {
			t.Errorf("stopping envtest: %v", err)
		}
	})
	apiClient, err := client.New(config, client.Options{})
	if err != nil {
		t.Fatalf("creating client: %v", err)
	}
	return apiClient
}
