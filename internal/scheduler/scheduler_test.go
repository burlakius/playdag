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
		s.Push(n, "")
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
		s.Push(nodeP(id, 0, nil), "")
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
			s.Push(n, "")
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
	s.Push(nodeP("t", 0, []string{"chromium"}), "")

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
	s.Push(nodeP("t", 0, []string{"webkit"}), "")

	if _, _, ok := s.Next(); ok {
		t.Fatal("expected no schedulable node for unmatched tag")
	}
	if s.Pending() != 1 {
		t.Fatalf("node should stay queued, pending=%d", s.Pending())
	}
}

func TestNext_SkipsBlockedHigherPriority(t *testing.T) {
	s := New([]*Agent{{Name: "chrome", Tags: []string{"chromium"}}}, nil)
	s.Push(nodeP("high", 100, []string{"webkit"}), "") // no agent can run it
	s.Push(nodeP("low", 1, []string{"chromium"}), "")

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
	s.Push(nodeP("x", 0, nil), "")
	s.Push(nodeP("y", 0, nil), "")

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

// An affine node runs on its own agent and nowhere else, even when another
// agent is idle and would take it happily: the session it needs exists only there.
func TestNext_AffinityPinsToOneAgent(t *testing.T) {
	a1 := &Agent{Name: "a1"}
	a2 := &Agent{Name: "a2"}
	s := New([]*Agent{a1, a2}, nil)
	s.Push(nodeP("pinned", 0, nil), "a2")

	n, agent, ok := s.Next()
	if !ok {
		t.Fatal("expected the node to be schedulable")
	}
	if n.TestID != "pinned" || agent.Name != "a2" {
		t.Fatalf("want pinned on a2, got %s on %s", n.TestID, agent.Name)
	}
}

func TestNext_AffineNodeWaitsForItsAgent(t *testing.T) {
	a1 := &Agent{Name: "a1"}
	a2 := &Agent{Name: "a2"}
	s := New([]*Agent{a1, a2}, nil)

	// Occupy a1, then queue a node that may only run on a1.
	s.Push(nodeP("busywork", 10, nil), "a1")
	if _, _, ok := s.Next(); !ok {
		t.Fatal("busywork should have taken a1")
	}
	s.Push(nodeP("pinned", 5, nil), "a1")

	if _, agent, ok := s.Next(); ok {
		t.Fatalf("pinned must wait for a1, but it was given %s", agent.Name)
	}
	if s.Pending() != 1 {
		t.Fatalf("pinned should stay queued, pending=%d", s.Pending())
	}

	// Once a1 is free it runs there.
	s.Release(a1)
	n, agent, ok := s.Next()
	if !ok || n.TestID != "pinned" || agent.Name != "a1" {
		t.Fatalf("want pinned on a1 after release, got %v on %v (ok=%v)", n, agent, ok)
	}
}

// A pinned node must not block an unpinned one that any agent can run.
func TestNext_AffineNodeDoesNotBlockOthers(t *testing.T) {
	a1 := &Agent{Name: "a1"}
	a2 := &Agent{Name: "a2"}
	s := New([]*Agent{a1, a2}, nil)

	s.Push(nodeP("busywork", 20, nil), "a1")
	s.Next()                               // takes a1
	s.Push(nodeP("pinned", 10, nil), "a1") // higher priority, but a1 is busy
	s.Push(nodeP("free", 1, nil), "")

	n, agent, ok := s.Next()
	if !ok || n.TestID != "free" || agent.Name != "a2" {
		t.Fatalf("want free on a2, got %v (ok=%v)", n, ok)
	}
}

func ids(nodes []*loader.TestNode) []string {
	out := make([]string, len(nodes))
	for i, n := range nodes {
		out[i] = n.TestID
	}
	return out
}

func TestNextBatch_TakesTheQueueInOrder(t *testing.T) {
	s := New([]*Agent{{Name: "a1"}}, nil)
	for _, n := range []*loader.TestNode{nodeP("A", 1, nil), nodeP("B", 10, nil), nodeP("C", 5, nil)} {
		s.Push(n, "")
	}

	nodes, agent, ok := s.NextBatch(2)
	if !ok || agent.Name != "a1" {
		t.Fatalf("NextBatch: ok=%v agent=%v", ok, agent)
	}
	if got := ids(nodes); !reflect.DeepEqual(got, []string{"B", "C"}) {
		t.Fatalf("got %v, want the two highest-priority nodes", got)
	}
	if s.Pending() != 1 {
		t.Errorf("pending = %d, want the untaken node still queued", s.Pending())
	}
}

// A batch is bound to one agent, so a node needing a tag that agent lacks has to
// wait rather than ride along.
func TestNextBatch_OnlyNodesTheAgentCanRun(t *testing.T) {
	s := New([]*Agent{{Name: "plain"}, {Name: "gpu", Tags: []string{"gpu"}}}, nil)
	s.Push(nodeP("plain-1", 10, nil), "")
	s.Push(nodeP("needs-gpu", 9, []string{"gpu"}), "")
	s.Push(nodeP("plain-2", 8, nil), "")

	nodes, agent, ok := s.NextBatch(4)
	if !ok {
		t.Fatal("NextBatch found nothing")
	}
	if agent.Name != "plain" {
		t.Fatalf("agent = %s, want the first free one", agent.Name)
	}
	if got := ids(nodes); !reflect.DeepEqual(got, []string{"plain-1", "plain-2"}) {
		t.Fatalf("got %v, want the tagged node left behind", got)
	}
}

// A node pinned to another agent holds its session there; batching it here would
// run it where its context does not exist.
func TestNextBatch_RespectsAffinity(t *testing.T) {
	s := New([]*Agent{{Name: "a1"}, {Name: "a2"}}, nil)
	s.Push(nodeP("free", 10, nil), "")
	s.Push(nodeP("pinned-a2", 9, nil), "a2")
	s.Push(nodeP("pinned-a1", 8, nil), "a1")

	nodes, agent, _ := s.NextBatch(4)
	if agent.Name != "a1" {
		t.Fatalf("agent = %s", agent.Name)
	}
	if got := ids(nodes); !reflect.DeepEqual(got, []string{"free", "pinned-a1"}) {
		t.Fatalf("got %v, want the node pinned elsewhere left behind", got)
	}
}

func TestNextBatch_EmptyQueue(t *testing.T) {
	s := New([]*Agent{{Name: "a1"}}, nil)
	if _, _, ok := s.NextBatch(4); ok {
		t.Fatal("an empty queue must yield nothing")
	}
}
