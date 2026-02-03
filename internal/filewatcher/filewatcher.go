// Package filewatcher provides a file system watcher that emits controller-runtime
// TypedGenericEvent when watched files change, enabling reconciliation triggers
// based on file modifications.
package filewatcher

import (
	"context"
	"fmt"
	"sync"

	"github.com/fsnotify/fsnotify"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

// Watcher wraps fsnotify and maps file paths to Kubernetes NamespacedName keys.
// It emits TypedGenericEvent[client.Object] for use with controller-runtime's
// source.Channel to trigger reconciliation when watched files change.
type Watcher struct {
	mu         sync.RWMutex
	watcher    *fsnotify.Watcher
	pathToKey  map[string]types.NamespacedName
	keyToPaths map[types.NamespacedName]map[string]struct{}
	events     chan event.TypedGenericEvent[client.Object]
	closed     bool
}

// New creates a new Watcher with a buffered event channel of the specified size.
func New(bufferSize int) (*Watcher, error) {
	fsWatcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("failed to create fsnotify watcher: %w", err)
	}

	return &Watcher{
		watcher:    fsWatcher,
		pathToKey:  make(map[string]types.NamespacedName),
		keyToPaths: make(map[types.NamespacedName]map[string]struct{}),
		events:     make(chan event.TypedGenericEvent[client.Object], bufferSize),
	}, nil
}

// Watch adds a path to the watcher and associates it with the given NamespacedName key.
// If the path is already watched with the same key, this is a no-op.
// If the path is already watched with a different key, an error is returned.
func (w *Watcher) Watch(path string, key types.NamespacedName) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Check if path is already watched
	if existingKey, exists := w.pathToKey[path]; exists {
		if existingKey == key {
			// Idempotent: same path with same key
			return nil
		}
		return fmt.Errorf("path %q already watched by %s/%s", path, existingKey.Namespace, existingKey.Name)
	}

	// Add to fsnotify
	if err := w.watcher.Add(path); err != nil {
		return fmt.Errorf("failed to watch path %q: %w", path, err)
	}

	// Update internal maps
	w.pathToKey[path] = key

	if w.keyToPaths[key] == nil {
		w.keyToPaths[key] = make(map[string]struct{})
	}
	w.keyToPaths[key][path] = struct{}{}

	return nil
}

// Unwatch removes a single path from the watcher.
func (w *Watcher) Unwatch(path string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	key, exists := w.pathToKey[path]
	if !exists {
		return nil // Not watching this path, no-op
	}

	// Remove from fsnotify
	if err := w.watcher.Remove(path); err != nil {
		return fmt.Errorf("failed to unwatch path %q: %w", path, err)
	}

	// Update internal maps
	delete(w.pathToKey, path)
	if paths, ok := w.keyToPaths[key]; ok {
		delete(paths, path)
		if len(paths) == 0 {
			delete(w.keyToPaths, key)
		}
	}

	return nil
}

// UnwatchAll removes all paths associated with the given NamespacedName key.
// This is typically used when a CR is deleted.
func (w *Watcher) UnwatchAll(key types.NamespacedName) {
	w.mu.Lock()
	defer w.mu.Unlock()

	paths, exists := w.keyToPaths[key]
	if !exists {
		return
	}

	for path := range paths {
		// Best effort removal from fsnotify - ignore errors
		_ = w.watcher.Remove(path)
		delete(w.pathToKey, path)
	}
	delete(w.keyToPaths, key)
}

// Events returns a receive-only channel of TypedGenericEvent for use with
// controller-runtime's source.Channel.
func (w *Watcher) Events() <-chan event.TypedGenericEvent[client.Object] {
	return w.events
}

// Start runs the event loop that converts fsnotify events to TypedGenericEvent.
// It blocks until the context is cancelled or Close is called.
func (w *Watcher) Start(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case fsEvent, ok := <-w.watcher.Events:
			if !ok {
				return nil // Watcher closed
			}
			w.handleFSEvent(fsEvent)

		case err, ok := <-w.watcher.Errors:
			if !ok {
				return nil // Watcher closed
			}
			// Log error but continue - fsnotify errors are typically transient
			// In production, this would use a logger
			_ = err
		}
	}
}

// handleFSEvent converts an fsnotify event to a TypedGenericEvent and sends it
// to the events channel.
func (w *Watcher) handleFSEvent(fsEvent fsnotify.Event) {
	// Only handle Write, Remove, and Rename events
	if !fsEvent.Has(fsnotify.Write) && !fsEvent.Has(fsnotify.Remove) && !fsEvent.Has(fsnotify.Rename) {
		return
	}

	w.mu.RLock()
	key, exists := w.pathToKey[fsEvent.Name]
	w.mu.RUnlock()

	if !exists {
		return
	}

	// Create a TypedGenericEvent with PartialObjectMetadata containing the key
	genericEvent := event.TypedGenericEvent[client.Object]{
		Object: &metav1.PartialObjectMetadata{
			ObjectMeta: metav1.ObjectMeta{
				Name:      key.Name,
				Namespace: key.Namespace,
			},
		},
	}

	// Non-blocking send to avoid blocking the event loop
	select {
	case w.events <- genericEvent:
	default:
		// Channel full, event dropped
		// In production, this would log a warning
	}
}

// Close stops the event loop and closes the underlying fsnotify watcher.
func (w *Watcher) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return nil
	}
	w.closed = true

	return w.watcher.Close()
}
