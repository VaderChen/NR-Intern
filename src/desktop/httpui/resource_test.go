package httpui

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveResourceURL(t *testing.T) {
	resource, err := resolveResource(resourceRequest{Kind: "url", Target: "https://example.com/docs?q=1", Action: "open"})
	if err != nil {
		t.Fatalf("resolveResource: %v", err)
	}
	if resource.Kind != "url" || resource.Target != "https://example.com/docs?q=1" || resource.Action != "open" {
		t.Fatalf("resource = %+v", resource)
	}
}

func TestResolveResourceRejectsUnsafeURLScheme(t *testing.T) {
	if _, err := resolveResource(resourceRequest{Kind: "url", Target: "file:///etc/passwd"}); err == nil {
		t.Fatal("file URL was accepted")
	}
}

func TestResolveResourcePath(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "HELLO.MD")
	if err := os.WriteFile(path, []byte("hello"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	resource, err := resolveResource(resourceRequest{Kind: "path", Target: path, Action: "reveal"})
	if err != nil {
		t.Fatalf("resolveResource: %v", err)
	}
	if resource.Target != path || resource.Directory || resource.Action != "reveal" {
		t.Fatalf("resource = %+v", resource)
	}
}

func TestResolveResourceRejectsRelativeAndMissingPaths(t *testing.T) {
	for _, target := range []string{"relative/file.txt", filepath.Join(t.TempDir(), "missing.txt")} {
		if _, err := resolveResource(resourceRequest{Kind: "path", Target: target}); err == nil {
			t.Fatalf("path %q was accepted", target)
		}
	}
}
