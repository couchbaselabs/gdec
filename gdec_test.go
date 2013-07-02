package gdec

import (
	"fmt"
	"testing"
)

func TestNewD(t *testing.T) {
	if NewD("") == nil {
		t.Errorf("expected D")
	}
}

func TestKV(t *testing.T) {
	d := KVInit(NewD(""), "")
	fmt.Printf("%#v\n", d)
}

func TestReplicatedKV(t *testing.T) {
	d := ReplicatedKVInit(NewD(""), "")
	fmt.Printf("%#v\n", d)
}

func TestQuorum(t *testing.T) {
	d := QuorumInit(NewD(""), "", 5, "")
	fmt.Printf("%#v\n", d)
}

func TestShortestPath(t *testing.T) {
	d := ShortestPathInit(NewD(""), "")
	links := d.Relations["ShortestPathLink"].(*LSet)
	paths := d.Relations["ShortestPath"].(*LSet)

	links.DirectAdd(&ShortestPathLink{From: "a", To: "b", Cost: 10})
	links.DirectAdd(&ShortestPathLink{From: "b", To: "c", Cost: 10})
	if links.Size() != 2 {
		t.Errorf("expected 2 links, got: %v", links.Size())
	}
	if paths.Size() != 0 {
		t.Errorf("expected 0 links, got: %v", paths.Size())
	}

	d.Tick()
	if d.ticks != 1 {
		t.Errorf("expected 1 ticks, got: %v", d.ticks)
	}
	if paths.Size() != 3 {
		t.Errorf("expected 3 links, got: %v, paths: %#v", paths.Size(), paths.m)
	}
	if !paths.Contains(&ShortestPath{From: "a", To: "c", Next: "b", Cost: 20}) {
		t.Errorf("expected paths to contain a->b")
	}

	d = ShortestPathInit(NewD(""), "")
	links = d.Relations["ShortestPathLink"].(*LSet)
	paths = d.Relations["ShortestPath"].(*LSet)
	links.DirectAdd(&ShortestPathLink{From: "a", To: "b", Cost: 10})
	links.DirectAdd(&ShortestPathLink{From: "b", To: "c", Cost: 10})
	links.DirectAdd(&ShortestPathLink{From: "a", To: "b", Cost: 1})
	d.Tick()
	if paths.Size() != 5 {
		t.Errorf("expected 5 links, got: %v, paths: %#v", paths.Size(), paths.m)
	}
	if !paths.Contains(&ShortestPath{From: "a", To: "c", Next: "b", Cost: 20}) {
		t.Errorf("expected paths to contain a->b")
	}
	if !paths.Contains(&ShortestPath{From: "a", To: "c", Next: "b", Cost: 11}) {
		t.Errorf("expected paths to contain a->b")
	}
	if paths.Contains(&ShortestPath{From: "a", To: "c", Next: "b", Cost: 1}) {
		t.Errorf("expected paths to to not contain a->b at the wrong cost")
	}
}
