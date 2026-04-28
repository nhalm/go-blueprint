package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

func main() {
	var (
		docsArg      = flag.String("docs", "", "comma-separated list of canonical docs to extract from")
		fixturesDir  = flag.String("fixtures", "examples/_smoke-fixtures", "directory containing go.mod.tmpl and other un-doc'd fixtures")
		templatesDir = flag.String("templates", "templates", "directory of templates/* to merge into the smoke output")
		module       = flag.String("module", "github.com/example/smoketest", "target module path (replaces github.com/yourorg/myapp in extracted content)")
		output       = flag.String("output", "", "output directory (will be created; refused if it exists unless --force)")
		force        = flag.Bool("force", false, "remove existing output directory before extracting")
		verbose      = flag.Bool("verbose", false, "print summary of extracted files")
	)
	flag.Parse()

	if *docsArg == "" {
		fmt.Fprintln(os.Stderr, "--docs is required")
		os.Exit(2)
	}
	if *output == "" {
		fmt.Fprintln(os.Stderr, "--output is required")
		os.Exit(2)
	}

	docs := strings.Split(*docsArg, ",")
	for i := range docs {
		docs[i] = strings.TrimSpace(docs[i])
	}

	if err := run(docs, *fixturesDir, *templatesDir, *module, *output, *force, *verbose); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(docs []string, fixturesDir, templatesDir, module, output string, force, verbose bool) error {
	if _, err := os.Stat(output); err == nil {
		if !force {
			return fmt.Errorf("output directory %q exists (pass --force to overwrite)", output)
		}
		if err := os.RemoveAll(output); err != nil {
			return fmt.Errorf("removing existing output: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("checking output: %w", err)
	}
	if err := os.MkdirAll(output, 0o755); err != nil {
		return fmt.Errorf("creating output: %w", err)
	}

	blocks, err := extractFromDocs(docs)
	if err != nil {
		return fmt.Errorf("extracting docs: %w", err)
	}
	for path, content := range blocks {
		out := stripLeadingPathComment(content, path)
		out = substituteModulePath(out, module)
		if err := writeFile(output, path, out); err != nil {
			return fmt.Errorf("writing %s: %w", path, err)
		}
	}

	if err := copyTreeWithSubstitution(templatesDir, output, module); err != nil {
		return fmt.Errorf("copying templates: %w", err)
	}

	tmpl := fixturesDir + "/go.mod.tmpl"
	if _, err := os.Stat(tmpl); err == nil {
		if err := renderGoModTemplate(tmpl, output, module); err != nil {
			return fmt.Errorf("rendering go.mod.tmpl: %w", err)
		}
	}

	if verbose {
		fmt.Printf("Extracted %d files from %d docs:\n", len(blocks), len(docs))
		debugDump(os.Stdout, blocks)
	}
	return nil
}
