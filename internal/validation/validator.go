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

func New() (*Validator, error) {
	validator := &Validator{compiled: make(map[Kind]*jsonschema.Schema, len(allKinds))}
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
		compiler := jsonschema.NewCompiler()
		compiler.AssertFormat()
		if err := compiler.AddResource(metadata.ID, document); err != nil {
			return nil, fmt.Errorf("register schema %s: %w", name, err)
		}
		compiled, err := compiler.Compile(metadata.ID)
		if err != nil {
			return nil, fmt.Errorf("compile schema %s: %w", name, err)
		}
		validator.compiled[kind] = compiled
	}
	return validator, nil
}

func (v *Validator) Validate(kind Kind, input []byte, filename string) error {
	if v == nil {
		return fmt.Errorf("validator is not initialized")
	}
	schema, ok := v.compiled[kind]
	if !ok {
		return fmt.Errorf("unsupported document kind %q", kind)
	}

	var document any
	var err error
	extension := strings.ToLower(filepath.Ext(filename))
	switch extension {
	case ".yaml", ".yml":
		if kind != ProductDefinition {
			return fmt.Errorf("YAML input is supported only for Product Definitions")
		}
		document, err = decodeYAML(input)
	default:
		document, err = decodeJSON(input)
	}
	if err != nil {
		return fmt.Errorf("decode %s: %w", filename, err)
	}
	if err := schema.Validate(document); err != nil {
		return fmt.Errorf("schema validation failed: %w", err)
	}
	return nil
}

func decodeJSON(input []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(input))
	var raw json.RawMessage
	if err := decoder.Decode(&raw); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("unexpected second JSON value")
		}
		return nil, fmt.Errorf("invalid trailing JSON: %w", err)
	}
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("invalid JSON document: %w", err)
	}
	return document, nil
}
