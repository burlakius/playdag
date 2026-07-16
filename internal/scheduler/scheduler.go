// Package scheduler orders ready test nodes for execution and binds them to
// agents. Ready nodes are held in a priority queue keyed by declared priority,
// critical-path length and test id; a node is only handed out to an agent whose
// tags satisfy the node's resource constraints.
package scheduler

import (
	"container/heap"

	"playdag/internal/loader"
)

// Agent is a concurrent execution slot advertising a set of capability tags.
type Agent struct {
	Name string   `json:"name"`
	Tags []string `json:"tags"`
}

func (a *Agent) satisfies(required []string) bool {
	for _, req := range required {
		if !contains(a.Tags, req) {
			return false
		}
	}
	return true
}

func contains(set []string, v string) bool {
	for _, s := range set {
		if s == v {
			return true
		}
	}
	return false
}

// Scheduler holds the ready queue and the agent pool. It is not safe for
// concurrent use: the runner drives it from a single goroutine.
type Scheduler struct {
	pq       priorityQueue
	critPath map[string]int
	agents   []*Agent
	busy     map[string]bool
}

// New returns a scheduler over the given agents. critPath maps a test id to its
// critical-path length and may be nil (all treated as zero).
func New(agents []*Agent, critPath map[string]int) *Scheduler {
	return &Scheduler{
		critPath: critPath,
		agents:   agents,
		busy:     make(map[string]bool, len(agents)),
	}
}

// Push enqueues a node that has become ready to run. affinity names the only
// agent that may run it, or is empty when any matching agent will do: a node
// needing a session-scoped value can only run where that value exists.
func (s *Scheduler) Push(node *loader.TestNode, affinity string) {
	heap.Push(&s.pq, &item{
		node:     node,
		priority: node.Policy.Priority,
		critPath: s.critPath[node.TestID],
		affinity: affinity,
	})
}

// Next reserves and returns the highest-priority ready node that a free agent
// can run, along with that agent. ok is false when nothing can run right now,
// either because the queue is empty or because no free agent matches a waiting
// node's tags.
func (s *Scheduler) Next() (node *loader.TestNode, agent *Agent, ok bool) {
	var deferred []*item
	for s.pq.Len() > 0 {
		it := heap.Pop(&s.pq).(*item)
		if a := s.freeAgentFor(it.node.Resources.Tags, it.affinity); a != nil {
			s.busy[a.Name] = true
			node, agent, ok = it.node, a, true
			break
		}
		deferred = append(deferred, it)
	}
	for _, it := range deferred {
		heap.Push(&s.pq, it)
	}
	return node, agent, ok
}

// freeAgentFor picks an idle agent that can run the node. A node with an
// affinity waits for that one agent rather than taking another: running it
// elsewhere would not find the session it needs.
func (s *Scheduler) freeAgentFor(required []string, affinity string) *Agent {
	for _, a := range s.agents {
		if affinity != "" && a.Name != affinity {
			continue
		}
		if !s.busy[a.Name] && a.satisfies(required) {
			return a
		}
	}
	return nil
}

// Release returns an agent to the pool.
func (s *Scheduler) Release(a *Agent) {
	delete(s.busy, a.Name)
}

// Pending reports how many ready nodes are still waiting for an agent.
func (s *Scheduler) Pending() int {
	return s.pq.Len()
}

// PendingTags lists the resource tags of the nodes still queued. It is used to
// explain a stall when no agent can satisfy any waiting node.
func (s *Scheduler) PendingTags() []string {
	var tags []string
	for _, it := range s.pq {
		tags = append(tags, it.node.Resources.Tags...)
	}
	return tags
}

// item is one queued node together with its ordering keys.
type item struct {
	node     *loader.TestNode
	priority int
	critPath int
	affinity string // agent that must run this node, or empty for any
}

// priorityQueue orders items by priority (desc), then critical-path length
// (desc), then test id (asc). The final key makes the order total, which is
// what keeps execution reproducible between runs.
type priorityQueue []*item

func (pq priorityQueue) Len() int { return len(pq) }

func (pq priorityQueue) Less(i, j int) bool {
	a, b := pq[i], pq[j]
	if a.priority != b.priority {
		return a.priority > b.priority
	}
	if a.critPath != b.critPath {
		return a.critPath > b.critPath
	}
	return a.node.TestID < b.node.TestID
}

func (pq priorityQueue) Swap(i, j int) { pq[i], pq[j] = pq[j], pq[i] }

func (pq *priorityQueue) Push(x any) {
	*pq = append(*pq, x.(*item))
}

func (pq *priorityQueue) Pop() any {
	old := *pq
	n := len(old)
	it := old[n-1]
	old[n-1] = nil
	*pq = old[:n-1]
	return it
}
