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
	switch strings.ToLower(filepath.Ext(filename)) {
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
