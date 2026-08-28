package valueutil

import (
	"errors"
	"testing"
)

func TestValueHelpersPreserveExistingSelectionSemantics(t *testing.T) {
	if got := FirstNonEmpty("  ", " value ", "later"); got != "value" {
		t.Fatalf("FirstNonEmpty = %q", got)
	}
	wanted := errors.New("first")
	if got := FirstError(nil, wanted, errors.New("later")); !errors.Is(got, wanted) {
		t.Fatalf("FirstError = %v", got)
	}
	original := map[string]any{"key": "value"}
	cloned := CloneMap(original)
	cloned["key"] = "changed"
	if original["key"] != "value" {
		t.Fatal("CloneMap returned the original map")
	}
}
