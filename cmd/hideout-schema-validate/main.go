package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: hideout-schema-validate <schema.json> <document.json>")
		return 2
	}
	schemaPath, documentPath := args[0], args[1]
	compiler, schemaURL, err := compilerForSchema(schemaPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "hideout-schema-validate: load schema: %v\n", err)
		return 1
	}
	schema, err := compiler.Compile(schemaURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "hideout-schema-validate: compile schema: %v\n", err)
		return 1
	}
	documentData, err := os.ReadFile(documentPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "hideout-schema-validate: read document: %v\n", err)
		return 1
	}
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(documentData))
	if err != nil {
		fmt.Fprintf(os.Stderr, "hideout-schema-validate: parse document: %v\n", err)
		return 1
	}
	if err := schema.Validate(document); err != nil {
		fmt.Fprintf(os.Stderr, "hideout-schema-validate: validate document: %v\n", err)
		return 1
	}
	return 0
}

func compilerForSchema(schemaPath string) (*jsonschema.Compiler, string, error) {
	absPath, err := filepath.Abs(schemaPath)
	if err != nil {
		return nil, "", err
	}
	paths, err := filepath.Glob(filepath.Join(filepath.Dir(absPath), "*.schema.json"))
	if err != nil {
		return nil, "", err
	}
	foundTarget := false
	for _, path := range paths {
		if filepath.Clean(path) == filepath.Clean(absPath) {
			foundTarget = true
			break
		}
	}
	if !foundTarget {
		paths = append(paths, absPath)
	}

	compiler := jsonschema.NewCompiler()
	targetURL := ""
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, "", fmt.Errorf("read %s: %w", path, err)
		}
		document, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
		if err != nil {
			return nil, "", fmt.Errorf("parse %s: %w", path, err)
		}
		resourceURL, err := schemaResourceURL(path, data)
		if err != nil {
			return nil, "", err
		}
		if err := compiler.AddResource(resourceURL, document); err != nil {
			return nil, "", fmt.Errorf("add %s: %w", path, err)
		}
		if filepath.Clean(path) == filepath.Clean(absPath) {
			targetURL = resourceURL
		}
	}
	if targetURL == "" {
		return nil, "", fmt.Errorf("target schema %s was not registered", absPath)
	}
	return compiler, targetURL, nil
}

func schemaResourceURL(path string, data []byte) (string, error) {
	var metadata struct {
		ID string `json:"$id"`
	}
	if err := json.Unmarshal(data, &metadata); err != nil {
		return "", fmt.Errorf("parse schema identity %s: %w", path, err)
	}
	if metadata.ID != "" {
		parsed, err := url.Parse(metadata.ID)
		if err != nil {
			return "", fmt.Errorf("parse schema $id %q: %w", metadata.ID, err)
		}
		if parsed.IsAbs() {
			return parsed.String(), nil
		}
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(absPath)}).String(), nil
}
