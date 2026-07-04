package main

import (
	"bytes"
	"fmt"
	"os"

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
	schemaData, err := os.ReadFile(schemaPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "hideout-schema-validate: read schema: %v\n", err)
		return 1
	}
	schemaDoc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemaData))
	if err != nil {
		fmt.Fprintf(os.Stderr, "hideout-schema-validate: parse schema: %v\n", err)
		return 1
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("schema.json", schemaDoc); err != nil {
		fmt.Fprintf(os.Stderr, "hideout-schema-validate: add schema: %v\n", err)
		return 1
	}
	schema, err := compiler.Compile("schema.json")
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
