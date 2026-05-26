package flow

import (
	"testing"
)

func TestBuildDAG_Simple(t *testing.T) {
	def := &Definition{
		Nodes: []NodeDef{
			{ID: "a", Type: "script"},
			{ID: "b", Type: "script"},
			{ID: "c", Type: "script"},
		},
		Edges: []EdgeDef{
			{From: "a", To: "b"},
			{From: "b", To: "c"},
		},
	}
	dag, err := BuildDAG(def)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got := len(dag.TopoSort); got != 3 {
		t.Fatalf("topo size: %d", got)
	}
	// a 必须在 b 之前
	posA, posB, posC := -1, -1, -1
	for i, id := range dag.TopoSort {
		switch id {
		case "a":
			posA = i
		case "b":
			posB = i
		case "c":
			posC = i
		}
	}
	if !(posA < posB && posB < posC) {
		t.Fatalf("topo order wrong: %v", dag.TopoSort)
	}
	entries := dag.EntryNodes()
	if len(entries) != 1 || entries[0] != "a" {
		t.Fatalf("entry nodes wrong: %v", entries)
	}
}

func TestBuildDAG_DetectsCycle(t *testing.T) {
	def := &Definition{
		Nodes: []NodeDef{
			{ID: "a", Type: "script"},
			{ID: "b", Type: "script"},
		},
		Edges: []EdgeDef{
			{From: "a", To: "b"},
			{From: "b", To: "a"},
		},
	}
	if _, err := BuildDAG(def); err == nil {
		t.Fatalf("expected cycle detection, got nil")
	}
}

func TestBuildDAG_DuplicateNode(t *testing.T) {
	def := &Definition{
		Nodes: []NodeDef{
			{ID: "a", Type: "script"},
			{ID: "a", Type: "script"},
		},
	}
	if _, err := BuildDAG(def); err == nil {
		t.Fatalf("expected duplicate id error")
	}
}

func TestBuildDAG_UnknownEdgeRef(t *testing.T) {
	def := &Definition{
		Nodes: []NodeDef{
			{ID: "a", Type: "script"},
		},
		Edges: []EdgeDef{
			{From: "a", To: "b"},
		},
	}
	if _, err := BuildDAG(def); err == nil {
		t.Fatalf("expected unknown ref error")
	}
}

func TestBuildDAG_StartEnd(t *testing.T) {
	def := &Definition{
		Nodes: []NodeDef{
			{ID: "a", Type: "script"},
		},
		Edges: []EdgeDef{
			{From: "__start__", To: "a"},
			{From: "a", To: "__end__"},
		},
	}
	dag, err := BuildDAG(def)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if e := dag.EntryNodes(); len(e) != 1 || e[0] != "a" {
		t.Fatalf("entry nodes: %v", e)
	}
}
