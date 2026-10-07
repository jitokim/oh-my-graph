package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// savePlanYAML writes the `--plan-only` preview's YAML copy of spec into dir,
// beside the graph.json saveGeneratedSpec wrote (ADR 0039 §9.1). It is the file
// the preview's closing note tells the user to edit and run, because a gate is
// authored, and editing YAML is the authoring path a hand-written graph has.
//
// Written owner-only for the reason graph.json is: it is the same content.
func savePlanYAML(dir string, spec []byte) (string, error) {
	out, err := specAsYAML(spec)
	if err != nil {
		return "", fmt.Errorf("render the plan as YAML: %w", err)
	}
	path := filepath.Join(dir, generatedSpecYAMLFileName)
	if err := os.WriteFile(path, out, 0o600); err != nil {
		return "", fmt.Errorf("save the plan as YAML: %w", err)
	}
	return path, nil
}

// specAsYAML is a projection of the planner's JSON spec, not a second source:
// JSON is valid YAML, so the spec decodes into a yaml.Node — keeping its key
// order and every scalar's resolved tag — and is re-encoded in block style.
// No struct sits in between, so no field can be dropped, renamed or defaulted
// on the way; graph.Load of the result is the same graph as graph.Load of the
// JSON (#342).
func specAsYAML(spec []byte) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(spec, &doc); err != nil {
		return nil, err
	}
	clearStyle(&doc)
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// clearStyle drops the flow and quoting styles JSON's syntax gave every node,
// so the encoder renders block YAML and quotes a scalar only where its tag
// would otherwise resolve differently ("1" stays a string, 1 stays a number).
func clearStyle(n *yaml.Node) {
	n.Style = 0
	for _, c := range n.Content {
		clearStyle(c)
	}
}
