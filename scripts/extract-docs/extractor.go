package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// fileBlock represents a single fenced code block annotated with {file=PATH}.
type fileBlock struct {
	sourceDoc string // relative path to the doc this block came from
	docOrder  int    // order within the source doc (0, 1, 2, ...)
	path      string // value of the {file=...} annotation
	content   string // raw block contents (no fence, no annotation)
}

// extractFromDocs parses every doc in docs and returns the accumulated
// {file=...} blocks keyed by path, with deterministic ordering across docs.
func extractFromDocs(docs []string) (map[string]string, error) {
	var blocks []fileBlock
	for _, doc := range docs {
		bs, err := parseDoc(doc)
		if err != nil {
			return nil, fmt.Errorf("parsing %s: %w", doc, err)
		}
		blocks = append(blocks, bs...)
	}

	// Deterministic ordering: alphabetical by source doc, then document order.
	sort.SliceStable(blocks, func(i, j int) bool {
		if blocks[i].sourceDoc != blocks[j].sourceDoc {
			return blocks[i].sourceDoc < blocks[j].sourceDoc
		}
		return blocks[i].docOrder < blocks[j].docOrder
	})

	out := make(map[string]string)
	for _, b := range blocks {
		if existing, ok := out[b.path]; ok {
			out[b.path] = existing + "\n\n" + b.content
		} else {
			out[b.path] = b.content
		}
	}
	return out, nil
}

// parseDoc reads a markdown file and returns every fenced code block annotated
// with {file=...}. Annotation must appear after the language tag on the opening
// fence line, e.g.: ```go {file=internal/models/product.go}
func parseDoc(path string) ([]fileBlock, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var blocks []fileBlock
	docOrder := 0
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 4*1024*1024)

	const fence = "```"
	inBlock := false
	var currentPath string
	var currentBuf strings.Builder

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if !inBlock {
			if !strings.HasPrefix(trimmed, fence) {
				continue
			}
			rest := strings.TrimPrefix(trimmed, fence)
			fpath, ok := parseFileAnnotation(rest)
			if !ok {
				// Code block without a {file=...} marker — skip it; we still
				// need to consume up to the closing fence so we don't pick up
				// inner backticks as a new opening fence.
				inBlock = true
				currentPath = ""
				currentBuf.Reset()
				continue
			}
			if strings.Contains(fpath, "..") {
				return nil, fmt.Errorf("%s: unsafe path %q (contains ..)", path, fpath)
			}
			inBlock = true
			currentPath = fpath
			currentBuf.Reset()
			continue
		}

		// Inside a block.
		if trimmed == fence {
			if currentPath != "" {
				blocks = append(blocks, fileBlock{
					sourceDoc: path,
					docOrder:  docOrder,
					path:      currentPath,
					content:   strings.TrimRight(currentBuf.String(), "\n"),
				})
				docOrder++
			}
			inBlock = false
			currentPath = ""
			currentBuf.Reset()
			continue
		}
		if currentPath != "" {
			currentBuf.WriteString(line)
			currentBuf.WriteByte('\n')
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if inBlock {
		return nil, fmt.Errorf("%s: unterminated code fence", path)
	}
	return blocks, nil
}

// parseFileAnnotation pulls the PATH out of `<lang> {file=PATH}` (or
// `{file=PATH}` with no lang). Returns ok=false if the annotation is absent.
func parseFileAnnotation(rest string) (string, bool) {
	open := strings.Index(rest, "{file=")
	if open < 0 {
		return "", false
	}
	rest = rest[open+len("{file="):]
	close := strings.Index(rest, "}")
	if close < 0 {
		return "", false
	}
	path := strings.TrimSpace(rest[:close])
	if path == "" {
		return "", false
	}
	return path, true
}

// substituteModulePath replaces every occurrence of "github.com/yourorg/myapp"
// with the target module path.
func substituteModulePath(content, target string) string {
	return strings.ReplaceAll(content, "github.com/yourorg/myapp", target)
}

// stripLeadingPathComment removes the redundant `// path/to/file.ext` line that
// many doc blocks open with — the {file=...} marker already encodes the path,
// and revive's package-comments rule rejects a leading non-Package-form comment
// in front of `package X`.
func stripLeadingPathComment(content, filePath string) string {
	lines := strings.SplitN(content, "\n", 2)
	if len(lines) == 0 {
		return content
	}
	first := strings.TrimSpace(lines[0])
	expected := "// " + filePath
	// Accept the exact path or path with a parenthetical suffix like "(generated)".
	if first == expected || strings.HasPrefix(first, expected+" ") {
		if len(lines) == 1 {
			return ""
		}
		return lines[1]
	}
	return content
}

// writeFile writes content to <outputDir>/<relPath>, creating parent dirs.
func writeFile(outputDir, relPath, content string) error {
	full := filepath.Join(outputDir, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	return os.WriteFile(full, []byte(content), 0o644)
}

// copyTreeWithSubstitution walks src and copies every file to dst, applying
// module-path substitution to text content. It does not follow symlinks.
func copyTreeWithSubstitution(src, dst, targetModule string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out := substituteModulePath(string(data), targetModule)
		return writeFile(dst, rel, out)
	})
}

// renderGoModTemplate reads go.mod.tmpl and writes go.mod with the target
// module path substituted for `BLUEPRINT_TARGET_MODULE`.
func renderGoModTemplate(tmplPath, outputDir, targetModule string) error {
	data, err := os.ReadFile(tmplPath)
	if err != nil {
		return err
	}
	rendered := strings.ReplaceAll(string(data), "BLUEPRINT_TARGET_MODULE", targetModule)
	return writeFile(outputDir, "go.mod", rendered)
}

// debugDump writes a summary of extracted blocks to w (used by --verbose).
func debugDump(w io.Writer, blocks map[string]string) {
	keys := make([]string, 0, len(blocks))
	for k := range blocks {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(w, "  %s (%d bytes)\n", k, len(blocks[k]))
	}
}
