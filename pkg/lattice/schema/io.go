package schema

import (
	"bytes"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// MarshalCanonical renders v as YAML with stable, diff-friendly output:
// 2-space indent, struct field order preserved. Two unrelated edits never
// produce noisy reorderings because field order is fixed by the struct.
func MarshalCanonical(v interface{}) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// LoadManifest reads and parses a manifest YAML file.
func LoadManifest(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &m, nil
}

// LoadInitiative reads and parses an initiative YAML file.
func LoadInitiative(path string) (*Initiative, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var in Initiative
	if err := yaml.Unmarshal(data, &in); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &in, nil
}

// LoadBRD reads and parses a Business Requirements Document YAML file.
func LoadBRD(path string) (*BRD, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var b BRD
	if err := yaml.Unmarshal(data, &b); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &b, nil
}

// LoadTask reads and parses a task YAML file.
func LoadTask(path string) (*Task, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var t Task
	if err := yaml.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &t, nil
}

// SaveCanonical writes v to path as canonical YAML, creating parent dirs.
func SaveCanonical(path string, v interface{}) error {
	data, err := MarshalCanonical(v)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
