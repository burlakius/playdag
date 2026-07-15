package scheduler

import (
	"reflect"
	"testing"

	"playdag/internal/loader"
)

func nodeP(id string, priority int, tags []string) *loader.TestNode {
	return &loader.TestNode{
		TestID:    id,
		Policy:    loader.ExecutionPolicy{Priority: priority},
		Resources: loader.ResourceConstraints{Tags: tags},
	}
}

// drain pops every schedulable node in order, releasing the agent each time so
// a single agent is enough to observe the full ordering.
func drain(s *Scheduler) []string {
	var order []string
	for {
		n, a, ok := s.Next()
		if !ok {
			break
		}
		order = append(order, n.TestID)
		s.Release(a)
	}
	return order
}

func TestQueueOrder_PriorityThenCritPathThenID(t *testing.T) {
	nodes := []*loader.TestNode{
		nodeP("A", 5, nil),
		nodeP("B", 10, nil),
		nodeP("C", 10, nil),
		nodeP("D", 1, nil),
	}
	crit := map[string]int{"B": 1, "C": 2} // C sits on a longer downstream chain
	s := New([]*Agent{{Name: "a1"}}, crit)
	for _, n := range nodes {
		s.Push(n)
	}

	got := drain(s)
	want := []string{"C", "B", "A", "D"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("order: want %v, got %v", want, got)
	}
}

func TestQueueOrder_TieBreakByID(t *testing.T) {
	s := New([]*Agent{{Name: "a1"}}, nil)
	for _, id := range []string{"z", "a", "m"} {
		s.Push(nodeP(id, 0, nil))
	}

	got := drain(s)
	want := []string{"a", "m", "z"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("order: want %v, got %v", want, got)
	}
}

func TestQueueOrder_Reproducible(t *testing.T) {
	build := func() []string {
		s := New([]*Agent{{Name: "a1"}}, map[string]int{"b": 3})
		for _, n := range []*loader.TestNode{
			nodeP("a", 5, nil), nodeP("b", 5, nil), nodeP("c", 9, nil),
		} {
			s.Push(n)
		}
		return drain(s)
	}
	if first, second := build(), build(); !reflect.DeepEqual(first, second) {
		t.Fatalf("order not reproducible: %v vs %v", first, second)
	}
}

func TestNext_TagMatching(t *testing.T) {
	chrome := &Agent{Name: "chrome", Tags: []string{"chromium"}}
	fire := &Agent{Name: "fire", Tags: []string{"firefox"}}
	s := New([]*Agent{chrome, fire}, nil)
	s.Push(nodeP("t", 0, []string{"chromium"}))

	n, a, ok := s.Next()
	if !ok {
		t.Fatal("expected a schedulable node")
	}
	if n.TestID != "t" || a.Name != "chrome" {
		t.Fatalf("expected t on chrome, got %s on %s", n.TestID, a.Name)
	}
}

func TestNext_NoMatchingAgent(t *testing.T) {
	s := New([]*Agent{{Name: "chrome", Tags: []string{"chromium"}}}, nil)
	s.Push(nodeP("t", 0, []string{"webkit"}))

	if _, _, ok := s.Next(); ok {
		t.Fatal("expected no schedulable node for unmatched tag")
	}
	if s.Pending() != 1 {
		t.Fatalf("node should stay queued, pending=%d", s.Pending())
	}
}

func TestNext_SkipsBlockedHigherPriority(t *testing.T) {
	s := New([]*Agent{{Name: "chrome", Tags: []string{"chromium"}}}, nil)
	s.Push(nodeP("high", 100, []string{"webkit"})) // no agent can run it
	s.Push(nodeP("low", 1, []string{"chromium"}))

	n, _, ok := s.Next()
	if !ok || n.TestID != "low" {
		t.Fatalf("expected low to run past blocked high, got %v ok=%v", n, ok)
	}
	if s.Pending() != 1 {
		t.Fatalf("high should remain queued, pending=%d", s.Pending())
	}
}

func TestNext_AllAgentsBusy(t *testing.T) {
	s := New([]*Agent{{Name: "a1"}}, nil)
	s.Push(nodeP("x", 0, nil))
	s.Push(nodeP("y", 0, nil))

	if _, _, ok := s.Next(); !ok {
		t.Fatal("first Next should succeed")
	}
	if _, _, ok := s.Next(); ok {
		t.Fatal("second Next should fail: the only agent is busy")
	}
	if s.Pending() != 1 {
		t.Fatalf("one node should remain queued, pending=%d", s.Pending())
	}
}

func TestParseAgents_Inline(t *testing.T) {
	agents, err := ParseAgents(`[{"name":"a1","tags":["chromium"]},{"name":"a2"}]`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(agents) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(agents))
	}
	if agents[0].Name != "a1" || !contains(agents[0].Tags, "chromium") {
		t.Fatalf("unexpected first agent: %+v", agents[0])
	}
}

func TestParseAgents_Empty(t *testing.T) {
	agents, err := ParseAgents("")
	if err != nil || agents != nil {
		t.Fatalf("empty spec should yield (nil, nil), got (%v, %v)", agents, err)
	}
}

func TestParseAgents_DuplicateName(t *testing.T) {
	if _, err := ParseAgents(`[{"name":"x"},{"name":"x"}]`); err == nil {
		t.Fatal("expected duplicate-name error")
	}
}

func TestParseAgents_MissingName(t *testing.T) {
	if _, err := ParseAgents(`[{"tags":["chromium"]}]`); err == nil {
		t.Fatal("expected missing-name error")
	}
}
