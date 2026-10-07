package graph

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// SpliceReuse resolves every `reuse:` node of a planner reply (ADR 0038 §2.3)
// with the same machinery that resolves a hand-written `use:` (ADR 0013, 0027,
// 0029): each citation becomes a `use:` of the named fragment and its `bind:`
// becomes the `with:` bindings, so a single-node shape is merged onto the
// citing node — which keeps the planner's id and depends_on — and a multi-node
// shape is spliced under the citing node's `/` namespace.
//
// pinned maps each cited name to the bytes of <graphDir>/fragments/<name>.yaml
// that the caller read and checked against its recorded digest. Those bytes are
// what gets spliced: the cited file is never read again here, so what was
// hashed is what runs. A pinned file whose own body holds a nested use: is
// refused before anything is resolved (#338): the file it cites was never
// hashed, so splicing it would run bytes nobody pinned, and it is not read.
//
// The returned graph is decoded but NOT validated: the caller runs its own
// per-node checks over the spliced nodes and then Graph.Validate, in that order.
func SpliceReuse(spec []byte, graphDir string, pinned map[string][]byte) (*Graph, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(spec, &doc); err != nil {
		return nil, fmt.Errorf("parse planned graph: %w", err)
	}
	nodes := findNodesSequence(&doc)
	if nodes == nil {
		return nil, fmt.Errorf("planned graph has no nodes sequence to splice into")
	}
	for _, nodeMap := range nodes.Content {
		if nodeMap.Kind != yaml.MappingNode {
			continue
		}
		keys := mappingValues(nodeMap)
		id := scalarValue(keys["id"])
		// A planner reply never carries use:/with: — ParsePlannerReply refused
		// it before this was reached — so finding one here means the reply
		// did not come through that path, and nothing it names is resolved.
		if keys["use"] != nil || keys["with"] != nil {
			return nil, fmt.Errorf("planned node %q carries use:/with:, which a planner reply may never carry; only reuse:/bind: are spliced", id)
		}
		reuse := keys["reuse"]
		if reuse == nil {
			if keys["bind"] != nil {
				return nil, fmt.Errorf("planned node %q carries bind: without reuse:", id)
			}
			continue
		}
		name := strings.TrimSpace(scalarValue(reuse))
		if _, ok := pinned[name]; !ok {
			return nil, fmt.Errorf("planned node %q cites the reusable shape %q, whose bytes were not pinned for the splice", id, name)
		}
		for i := 0; i+1 < len(nodeMap.Content); i += 2 {
			switch key := nodeMap.Content[i]; key.Value {
			case "reuse":
				key.Value = "use"
			case "bind":
				key.Value = "with"
			}
		}
	}

	names := make([]string, 0, len(pinned))
	for name := range pinned {
		names = append(names, name)
	}
	sort.Strings(names)
	cache := make(map[string]*loadedFragment, len(pinned))
	for _, name := range names {
		if err := bareFragmentName(name); err != nil {
			return nil, err
		}
		lf := loadFragmentData(name, filepath.Join(graphDir, "fragments", name+".yaml"), pinned[name])
		if lf.frag == nil {
			return nil, lf.errs[0]
		}
		if nested := nestedUses(lf.frag); len(nested) > 0 {
			return nil, fmt.Errorf("the reusable shape %q holds a nested use: (%s), and only the shape's own bytes were pinned, so it is refused rather than the cited fragment read from disk", name, nested[0])
		}
		cache[name] = lf
	}
	entryPath := filepath.Join(graphDir, "reuse-splice.yaml") // never read: only its directory would anchor a lookup, and none remains
	outcome := resolveFragmentsWith(&doc, entryPath, cache)
	if len(outcome.errs) > 0 {
		return nil, outcome.errs[0]
	}
	return decodeResolved(&doc)
}
