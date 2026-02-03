package filewatcher_test

import (
	"context"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"

	"github.com/isoboot/isoboot/internal/filewatcher"
)

var _ = Describe("Watcher", func() {
	var (
		w       *filewatcher.Watcher
		tempDir string
		ctx     context.Context
		cancel  context.CancelFunc
	)

	BeforeEach(func() {
		var err error
		w, err = filewatcher.New(10)
		Expect(err).NotTo(HaveOccurred())

		tempDir = GinkgoT().TempDir()
		ctx, cancel = context.WithCancel(context.Background())
	})

	AfterEach(func() {
		cancel()
		if w != nil {
			_ = w.Close()
		}
	})

	Describe("Watch", func() {
		It("adds path to fsnotify", func() {
			testFile := filepath.Join(tempDir, "test.txt")
			Expect(os.WriteFile(testFile, []byte("initial"), 0644)).To(Succeed())

			key := types.NamespacedName{Namespace: "default", Name: "test-cr"}
			err := w.Watch(testFile, key)
			Expect(err).NotTo(HaveOccurred())
		})

		It("is idempotent when watching same path with same key", func() {
			testFile := filepath.Join(tempDir, "test.txt")
			Expect(os.WriteFile(testFile, []byte("initial"), 0644)).To(Succeed())

			key := types.NamespacedName{Namespace: "default", Name: "test-cr"}

			err := w.Watch(testFile, key)
			Expect(err).NotTo(HaveOccurred())

			// Second call with same key should succeed (idempotent)
			err = w.Watch(testFile, key)
			Expect(err).NotTo(HaveOccurred())
		})

		It("returns error when watching same path with different key", func() {
			testFile := filepath.Join(tempDir, "test.txt")
			Expect(os.WriteFile(testFile, []byte("initial"), 0644)).To(Succeed())

			key1 := types.NamespacedName{Namespace: "default", Name: "test-cr-1"}
			key2 := types.NamespacedName{Namespace: "default", Name: "test-cr-2"}

			err := w.Watch(testFile, key1)
			Expect(err).NotTo(HaveOccurred())

			// Second call with different key should fail
			err = w.Watch(testFile, key2)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("already watched"))
		})
	})

	Describe("Unwatch", func() {
		It("removes path from watcher", func() {
			testFile := filepath.Join(tempDir, "test.txt")
			Expect(os.WriteFile(testFile, []byte("initial"), 0644)).To(Succeed())

			key := types.NamespacedName{Namespace: "default", Name: "test-cr"}

			err := w.Watch(testFile, key)
			Expect(err).NotTo(HaveOccurred())

			err = w.Unwatch(testFile)
			Expect(err).NotTo(HaveOccurred())

			// Should be able to watch again with a different key after unwatch
			key2 := types.NamespacedName{Namespace: "default", Name: "test-cr-2"}
			err = w.Watch(testFile, key2)
			Expect(err).NotTo(HaveOccurred())
		})

		It("is a no-op for unwatched paths", func() {
			testFile := filepath.Join(tempDir, "nonexistent.txt")
			err := w.Unwatch(testFile)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Describe("UnwatchAll", func() {
		It("removes all paths for a key", func() {
			file1 := filepath.Join(tempDir, "file1.txt")
			file2 := filepath.Join(tempDir, "file2.txt")
			Expect(os.WriteFile(file1, []byte("content1"), 0644)).To(Succeed())
			Expect(os.WriteFile(file2, []byte("content2"), 0644)).To(Succeed())

			key := types.NamespacedName{Namespace: "default", Name: "test-cr"}

			Expect(w.Watch(file1, key)).To(Succeed())
			Expect(w.Watch(file2, key)).To(Succeed())

			w.UnwatchAll(key)

			// Both paths should be available for new keys
			key2 := types.NamespacedName{Namespace: "default", Name: "test-cr-2"}
			Expect(w.Watch(file1, key2)).To(Succeed())
			Expect(w.Watch(file2, key2)).To(Succeed())
		})

		It("is a no-op for unknown keys", func() {
			key := types.NamespacedName{Namespace: "default", Name: "unknown"}
			// Should not panic
			w.UnwatchAll(key)
		})
	})

	Describe("Event emission", func() {
		var (
			eventsChan <-chan event.TypedGenericEvent[client.Object]
			testFile   string
			key        types.NamespacedName
		)

		BeforeEach(func() {
			testFile = filepath.Join(tempDir, "watched.txt")
			Expect(os.WriteFile(testFile, []byte("initial"), 0644)).To(Succeed())

			key = types.NamespacedName{Namespace: "test-ns", Name: "test-resource"}

			Expect(w.Watch(testFile, key)).To(Succeed())
			eventsChan = w.Events()

			// Start the watcher in a goroutine
			go func() {
				defer GinkgoRecover()
				_ = w.Start(ctx)
			}()

			// Give the watcher time to start
			time.Sleep(50 * time.Millisecond)
		})

		It("triggers event on file write", func() {
			// Modify the file
			Expect(os.WriteFile(testFile, []byte("modified"), 0644)).To(Succeed())

			var received event.TypedGenericEvent[client.Object]
			Eventually(eventsChan, 2*time.Second).Should(Receive(&received))
		})

		It("triggers event on file delete", func() {
			// Delete the file
			Expect(os.Remove(testFile)).To(Succeed())

			var received event.TypedGenericEvent[client.Object]
			Eventually(eventsChan, 2*time.Second).Should(Receive(&received))
		})

		It("contains correct NamespacedName in event", func() {
			// Modify the file
			Expect(os.WriteFile(testFile, []byte("modified"), 0644)).To(Succeed())

			var received event.TypedGenericEvent[client.Object]
			Eventually(eventsChan, 2*time.Second).Should(Receive(&received))

			Expect(received.Object).NotTo(BeNil())
			Expect(received.Object.GetName()).To(Equal(key.Name))
			Expect(received.Object.GetNamespace()).To(Equal(key.Namespace))
		})
	})

	Describe("Close", func() {
		It("stops the event loop", func() {
			startReturned := make(chan struct{})

			go func() {
				defer GinkgoRecover()
				_ = w.Start(ctx)
				close(startReturned)
			}()

			// Give Start time to begin
			time.Sleep(50 * time.Millisecond)

			// Close should cause Start to return
			Expect(w.Close()).To(Succeed())

			Eventually(startReturned, 2*time.Second).Should(BeClosed())
		})

		It("is idempotent", func() {
			Expect(w.Close()).To(Succeed())
			Expect(w.Close()).To(Succeed())
		})
	})
})
