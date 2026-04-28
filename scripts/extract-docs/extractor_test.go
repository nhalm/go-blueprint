package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testdataDoc = "testdata/minimal-doc.md"

func TestExtractFromDocs_AccumulatesBlocks(t *testing.T) {
	blocks, err := extractFromDocs([]string{testdataDoc})
	if err != nil {
		t.Fatalf("extract: %v", err)
	}

	want := map[string]bool{
		"internal/models/product.go": true,
		"schema.sql":                 true,
		"internal/api/handler.go":    true,
	}
	for path := range want {
		if _, ok := blocks[path]; !ok {
			t.Errorf("missing %s", path)
		}
	}
	if len(blocks) != len(want) {
		t.Errorf("extracted %d files, want %d: got keys=%v", len(blocks), len(want), keys(blocks))
	}
}

func TestExtractFromDocs_ConcatenatesSameFileBlocks(t *testing.T) {
	blocks, err := extractFromDocs([]string{testdataDoc})
	if err != nil {
		t.Fatalf("extract: %v", err)
	}

	got := blocks["internal/models/product.go"]
	if !strings.Contains(got, "type Product struct") {
		t.Errorf("missing first block content; got %q", got)
	}
	if !strings.Contains(got, "type Account struct") {
		t.Errorf("missing second block content; got %q", got)
	}
	// Order: first block (Product) must precede second block (Account).
	if strings.Index(got, "type Product struct") > strings.Index(got, "type Account struct") {
		t.Errorf("block order reversed; got %q", got)
	}
}

func TestExtractFromDocs_IgnoresUnmarkedBlocks(t *testing.T) {
	blocks, err := extractFromDocs([]string{testdataDoc})
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	for _, content := range blocks {
		if strings.Contains(content, "package illustrative") {
			t.Errorf("unmarked illustrative block was extracted; should be skipped")
		}
	}
}

func TestExtractFromDocs_RejectsPathTraversal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.md")
	body := "```go {file=../../../etc/passwd}\npackage evil\n```\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := extractFromDocs([]string{path}); err == nil {
		t.Errorf("expected error for path traversal, got nil")
	}
}

func TestSubstituteModulePath(t *testing.T) {
	in := `import "github.com/yourorg/myapp/internal/models"`
	got := substituteModulePath(in, "github.com/example/smoketest")
	want := `import "github.com/example/smoketest/internal/models"`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestExtractFromDocs_ProseOnlyEditsAreStable codifies the Q8 invariant: prose
// edits to source docs must not change extractor output. Mutating only prose
// (text outside fenced blocks) leaves the per-file content byte-identical.
func TestExtractFromDocs_ProseOnlyEditsAreStable(t *testing.T) {
	original, err := os.ReadFile(testdataDoc)
	if err != nil {
		t.Fatal(err)
	}
	before, err := extractFromDocs([]string{testdataDoc})
	if err != nil {
		t.Fatalf("extract before: %v", err)
	}

	dir := t.TempDir()
	mutated := filepath.Join(dir, "mutated.md")
	body := strings.Replace(string(original), "Some prose.", "Completely different prose with new wording.", 1)
	body = strings.Replace(body, "More prose between blocks.", "An entirely rewritten paragraph.", 1)
	if body == string(original) {
		t.Fatal("test mutation produced no change; testdata changed")
	}
	if err := os.WriteFile(mutated, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	after, err := extractFromDocs([]string{mutated})
	if err != nil {
		t.Fatalf("extract after: %v", err)
	}

	if len(before) != len(after) {
		t.Fatalf("file count changed: before=%d after=%d", len(before), len(after))
	}
	for path, beforeContent := range before {
		afterContent, ok := after[path]
		if !ok {
			t.Errorf("file %s missing after prose edit", path)
			continue
		}
		if beforeContent != afterContent {
			t.Errorf("file %s changed after prose edit:\nbefore=%q\nafter=%q", path, beforeContent, afterContent)
		}
	}
}

func TestParseFileAnnotation(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"go {file=foo.go}", "foo.go", true},
		{"sql {file=path/to/file.sql}", "path/to/file.sql", true},
		{"{file=bare.go}", "bare.go", true},
		{"go", "", false},
		{"go {file=}", "", false},
		{"go {other=x}", "", false},
	}
	for _, c := range cases {
		got, ok := parseFileAnnotation(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("parseFileAnnotation(%q) = (%q,%t), want (%q,%t)", c.in, got, ok, c.want, c.ok)
		}
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
