// Command testdatagen creates a deterministic, synthetic legacy dataset for
// migration verification. It never reads from an existing database or any
// external system.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Hao-Monster/Xboard-Go/internal/testdata/legacy/gen"
)

func main() {
	flags := flag.NewFlagSet("testdatagen", flag.ExitOnError)
	outputDir := flags.String("output", "", "directory for the generated dataset")
	if err := flags.Parse(os.Args[1:]); err != nil {
		fail(err)
	}
	if *outputDir == "" || flags.NArg() != 0 {
		fail(fmt.Errorf("usage: testdatagen --output DIRECTORY"))
	}

	if err := os.MkdirAll(*outputDir, 0o700); err != nil {
		fail(fmt.Errorf("create output directory: %w", err))
	}
	databasePath := filepath.Join(*outputDir, "legacy.db")
	manifestPath := filepath.Join(*outputDir, "manifest.json")
	manifest, err := gen.New(gen.DefaultConfig(databasePath)).Generate(context.Background())
	if err != nil {
		fail(err)
	}
	if err := gen.WriteManifest(manifest, manifestPath); err != nil {
		fail(err)
	}
	fmt.Printf("generated %s (%s)\n", databasePath, manifest.DatabaseSHA)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(2)
}
