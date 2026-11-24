package main

import (
	"slices"
	"testing"
)

func TestQueuePositions(t *testing.T) {
	p := &player{queue: []track{{id: "a"}, {id: "b"}, {id: "c"}}}
	p.reorderQueue("move", []string{"1", "3"})
	p.reorderQueue("remove", []string{"2"})
	got := []string{p.queue[0].id, p.queue[1].id}
	if !slices.Equal(got, []string{"b", "a"}) {
		t.Fatalf("queue is %v, want [b a]", got)
	}
	p.reorderQueue("move", []string{"1", "3"})
	if p.queue[0].id != "b" {
		t.Fatal("invalid position changed queue")
	}
}
