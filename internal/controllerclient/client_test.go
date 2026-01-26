package controllerclient

import (
	"errors"
	"fmt"
	"testing"
)

func TestErrNotFound_IsComparable(t *testing.T) {
	// Verify ErrNotFound can be used with errors.Is when wrapped
	wrappedErr := fmt.Errorf("boottarget foo: %w", ErrNotFound)

	if !errors.Is(wrappedErr, ErrNotFound) {
		t.Error("expected errors.Is(wrappedErr, ErrNotFound) to be true")
	}
}

func TestErrNotFound_NotMatchOther(t *testing.T) {
	otherErr := errors.New("some other error")

	if errors.Is(otherErr, ErrNotFound) {
		t.Error("expected errors.Is(otherErr, ErrNotFound) to be false")
	}
}

func TestErrNotFound_ErrorMessage(t *testing.T) {
	if ErrNotFound.Error() != "not found" {
		t.Errorf("expected error message 'not found', got %q", ErrNotFound.Error())
	}
}
