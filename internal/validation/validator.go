package validation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/cdin-editor/registry/internal/schemas"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

type Validator struct {
	compiled map[Kind]*jsonschema.Schema
}

// New compiles every embedded Spec-owned schema and registers cross-schema
// references locally, without network schema retrieval (REG-005/027).
// Spec: https://github.com/cdin-editor/spec/blob/main/docs/architecture/registry-contracts.md#1-contract-inventory
// Design: https://github.com/cdin-editor/registry/blob/main/docs/implementation/catalog-ingestion.md.
func New() (*Validator, error) {
	validator := &Validator{compiled: make(map[Kind]*jsonschema.Schema, len(allKinds))}
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()

	// Register every Spec-owned schema before compiling any of them. This lets
	// public documents reuse other versioned contracts through absolute $ref
	// identifiers without fetching schemas over the network.
	schemaIDs := make(map[Kind]string, len(allKinds))
	for _, kind := range allKinds {
		name := "registry/v1/" + string(kind) + ".schema.json"
		data, err := schemas.FS.ReadFile(name)
		if err != nil {
			return nil, fmt.Errorf("read embedded schema %s: %w", name, err)
		}

		var metadata struct {
			ID string `json:"$id"`
		}
		if err := json.Unmarshal(data, &metadata); err != nil {
			return nil, fmt.Errorf("decode schema metadata %s: %w", name, err)
		}
		if metadata.ID == "" {
			return nil, fmt.Errorf("schema %s has no $id", name)
		}

		document, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("parse schema %s: %w", name, err)
		}
		if err := compiler.AddResource(metadata.ID, document); err != nil {
			return nil, fmt.Errorf("register schema %s: %w", name, err)
		}
		schemaIDs[kind] = metadata.ID
	}

	for _, kind := range allKinds {
		compiled, err := compiler.Compile(schemaIDs[kind])
		if err != nil {
			return nil, fmt.Errorf("compile schema %s: %w", kind, err)
		}
		validator.compiled[kind] = compiled
	}
	return validator, nil
}

// Validate checks strict JSON/YAML syntax and the Spec-owned schema for one
// public Registry document (REG-005). Use Normalize when the validated JSON
// data model must also be consumed by the resolver or another core package.
func (v *Validator) Validate(kind Kind, input []byte, filename string) error {
	_, err := v.Normalize(kind, input, filename)
	return err
}

// Normalize performs the same safe decoding and schema validation as Validate,
// then emits the validated JSON data model. Product Definition YAML is parsed
// once and normalized to JSON so CLI adapters do not reimplement YAML semantics
// or bypass duplicate-key/tag rejection (REG-001/005).
// Spec: https://github.com/cdin-editor/spec/blob/main/docs/architecture/registry-contracts.md#3-product-definition
// Design: https://github.com/cdin-editor/registry/blob/main/docs/implementation/catalog-ingestion.md.
func (v *Validator) Normalize(kind Kind, input []byte, filename string) ([]byte, error) {
	if v == nil {
		return nil, fmt.Errorf("validator is not initialized")
	}
	schema, ok := v.compiled[kind]
	if !ok {
		return nil, fmt.Errorf("unsupported document kind %q", kind)
	}
	var document any
	var err error
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".yaml", ".yml":
		if kind != ProductDefinition {
			return nil, fmt.Errorf("YAML input is supported only for Product Definitions")
		}
		document, err = decodeYAML(input)
	default:
		document, err = decodeJSON(input)
	}
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", filename, err)
	}
	if err := schema.Validate(document); err != nil {
		return nil, fmt.Errorf("schema validation failed: %w", err)
	}
	normalized, err := json.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("normalize %s to JSON: %w", filename, err)
	}
	return normalized, nil
}

func decodeJSON(input []byte) (any, error) {
	scanner := json.NewDecoder(bytes.NewReader(input))
	scanner.UseNumber()
	if err := scanJSONValue(scanner); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	if _, err := scanner.Token(); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("unexpected trailing JSON value")
		}
		return nil, fmt.Errorf("invalid trailing JSON: %w", err)
	}

	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(input))
	if err != nil {
		return nil, fmt.Errorf("invalid JSON document: %w", err)
	}
	return document, nil
}

func scanJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}

	switch delim {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("object key is not a string")
			}
			if _, exists := seen[key]; exists {
				return fmt.Errorf("duplicate JSON object key %q", key)
			}
			seen[key] = struct{}{}
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
		return expectJSONDelimiter(decoder, '}')

	case '[':
		for decoder.More() {
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
		return expectJSONDelimiter(decoder, ']')

	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delim)
	}
}

func expectJSONDelimiter(decoder *json.Decoder, expected json.Delim) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	actual, ok := token.(json.Delim)
	if !ok || actual != expected {
		return fmt.Errorf("expected JSON delimiter %q", expected)
	}
	return nil
}
