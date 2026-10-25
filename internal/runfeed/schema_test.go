package runfeed

import (
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"sort"
	"testing"
)

// TestNoRemoteChangeEvent (ADR 0046 §8.14): the remote change record lives in
// state.json only. The feed schema is still 3 and the closed type set is the
// one schema 3 defines — a new type would need a bump (see Schema).
func TestNoRemoteChangeEvent(t *testing.T) {
	if Schema != 3 {
		t.Errorf("feed Schema = %d, want 3", Schema)
	}
	file, err := parser.ParseFile(token.NewFileSet(), "runfeed.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs := spec.(*ast.ValueSpec)
			if id, ok := vs.Type.(*ast.Ident); ok && id.Name == "EventType" {
				for _, name := range vs.Names {
					got = append(got, name.Name)
				}
			}
		}
	}
	sort.Strings(got)
	want := []string{
		"EventGateApproved", "EventGatePaused", "EventGateRejected",
		"EventNodeFailed", "EventNodePassed", "EventNodeRetried", "EventNodeStarted",
		"EventRunFinished", "EventRunStarted",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("EventType constants = %v\nwant %v", got, want)
	}
}
