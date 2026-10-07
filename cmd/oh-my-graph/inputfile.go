package main

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// loadInputFile reads one `--input-file` path and returns its bindings, key to
// value, exactly as a run of `--input k=v` flags would have bound them (#354).
//
// The file is a flat map, YAML or JSON alike — both go through yaml.v3's node
// API, never encoding/json and never a decode into Go values. That is what
// keeps a value verbatim: each one is its scalar's source text
// (yaml.Node.Value), so 1.10 stays 1.10, 0123 stays 0123 and yes stays yes,
// where a decode would have turned them into 1.1, 83 and true and formatted
// them back. Kind and Tag are read only to refuse what has no text form for a
// `--input` value to take: a nested map, a list, a null (an empty `k:` among
// them) or an alias.
//
// A key binds only if `--input` could have bound it: a non-empty string with
// no '=' (inputFlag.Set splits at the first one). A key that appears twice is
// refused rather than last-one-wins — yaml.v3 does not refuse it when it
// unmarshals into a Node, so the mapping's pairs are walked here, and a .json
// file gets the same check. Every refusal names the path.
func loadInputFile(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("invalid --input-file %q: %w", path, err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("invalid --input-file %q: not valid YAML or JSON: %w", path, err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil, fmt.Errorf("invalid --input-file %q: the top level is an empty document (want a map of key: value)", path)
	}
	top := doc.Content[0]
	if top.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("invalid --input-file %q: the top level is %s (want a map of key: value)", path, describeInputNode(top))
	}
	bindings := make(map[string]string, len(top.Content)/2)
	for i := 0; i+1 < len(top.Content); i += 2 {
		key, value := top.Content[i], top.Content[i+1]
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
			return nil, fmt.Errorf("invalid --input-file %q: a key on line %d is %s, not a string", path, key.Line, describeInputNode(key))
		}
		if key.Value == "" || strings.Contains(key.Value, "=") {
			return nil, fmt.Errorf("invalid --input-file %q: key %q (want a non-empty key with no '=')", path, key.Value)
		}
		if _, dup := bindings[key.Value]; dup {
			return nil, fmt.Errorf("invalid --input-file %q: key %q appears more than once", path, key.Value)
		}
		if value.Kind != yaml.ScalarNode || value.Tag == "!!null" {
			return nil, fmt.Errorf("invalid --input-file %q: key %q is %s (want a single value)", path, key.Value, describeInputNode(value))
		}
		bindings[key.Value] = value.Value
	}
	return bindings, nil
}

// describeInputNode names what a refused node is, for loadInputFile's errors.
func describeInputNode(n *yaml.Node) string {
	switch {
	case n.Kind == yaml.MappingNode:
		return "a map (nested)"
	case n.Kind == yaml.SequenceNode:
		return "a list"
	case n.Kind == yaml.AliasNode:
		return "an alias"
	case n.Tag == "!!null":
		return "null"
	case n.Kind == yaml.ScalarNode:
		return "a scalar"
	default:
		return "not a value"
	}
}
