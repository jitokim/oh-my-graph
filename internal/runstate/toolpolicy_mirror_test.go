package runstate

import (
	"reflect"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/runner"
)

// TestNodeToolPolicyMirrorsRunnerToolPolicy guards a deliberate duplication.
//
// NodeToolPolicy is a copy of runner.ToolPolicy that exists on purpose: the
// snapshot on disk is a persisted contract, and tying it to a runtime type
// would make every refactor a schema migration. The cost of that independence
// is that nothing makes the copy keep up. A sixth layer added to the ceiling
// and not to the snapshot would not fail to compile, would not fail a test, and
// would surface only as a resumed planned node running one layer weaker than
// the leg that started it — the exact silent widening the ceiling exists to
// prevent, in the leg nobody watches.
//
// So the two shapes are compared by reflection: same field names, same types.
// The import is test-only, so production still has no runstate -> runner
// dependency and the snapshot format stays independent.
func TestNodeToolPolicyMirrorsRunnerToolPolicy(t *testing.T) {
	snapshot := fieldTypesByName(reflect.TypeOf(NodeToolPolicy{}))
	runtime := fieldTypesByName(reflect.TypeOf(runner.ToolPolicy{}))

	for name, runtimeType := range runtime {
		snapshotType, present := snapshot[name]
		if !present {
			t.Errorf(
				"runner.ToolPolicy.%s has no counterpart in runstate.NodeToolPolicy.\n"+
					"A ceiling layer that is not snapshotted is silently dropped when a run resumes.\n"+
					"Add the field here AND to the conversion at the resume boundary.",
				name)
			continue
		}
		if snapshotType != runtimeType {
			t.Errorf("field %s: snapshot has %s, runtime has %s — a round-trip would lose or corrupt it",
				name, snapshotType, runtimeType)
		}
	}

	for name := range snapshot {
		if _, present := runtime[name]; !present {
			t.Errorf("runstate.NodeToolPolicy.%s no longer exists on runner.ToolPolicy — the snapshot shape is stale", name)
		}
	}
}

func fieldTypesByName(typ reflect.Type) map[string]reflect.Type {
	fields := make(map[string]reflect.Type)
	for _, field := range reflect.VisibleFields(typ) {
		if field.IsExported() {
			fields[field.Name] = field.Type
		}
	}
	return fields
}
