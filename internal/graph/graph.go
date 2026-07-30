// Package graph builds and manages a DAG of test nodes for ordered execution.
package graph

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"playdag/internal/loader"
)

type NodeStatus int

const (
	StatusPending NodeStatus = iota
	StatusReady
	StatusRunning
	StatusPassed
	StatusFailed
	StatusSkipped
)

// DAG holds the dependency graph and runtime state for a test suite.
type DAG struct {
	nodes    map[string]*loader.TestNode
	edges    map[string][]string // node_id -> dependant node_ids
	inDegree map[string]int
	status   map[string]NodeStatus
	mu       sync.RWMutex
}

// NewDAG constructs a DAG from the given nodes, validating that all required
// keys are provided and that no cycles exist.
func NewDAG(nodes []loader.TestNode) (*DAG, error) {
	d := &DAG{
		nodes:    make(map[string]*loader.TestNode, len(nodes)),
		edges:    make(map[string][]string, len(nodes)),
		inDegree: make(map[string]int, len(nodes)),
		status:   make(map[string]NodeStatus, len(nodes)),
	}

	// Index nodes and initialise state.
	for i := range nodes {
		n := &nodes[i]
		d.nodes[n.TestID] = n
		d.edges[n.TestID] = nil
		d.inDegree[n.TestID] = 0
		d.status[n.TestID] = StatusPending
	}

	// Build a map from provided key -> node_id that produces it. A key may have
	// exactly one provider; a collision means the suite is ambiguous.
	providers := make(map[string]string)
	for _, n := range nodes {
		for _, key := range n.Contract.Provides {
			if owner, taken := providers[key]; taken {
				return nil, fmt.Errorf("key %q provided by both %q and %q", key, owner, n.TestID)
			}
			providers[key] = n.TestID
		}
	}

	// Wire edges: for each required key, add an edge from provider -> consumer.
	for _, n := range nodes {
		for _, key := range n.Contract.Requires {
			providerID, ok := providers[key]
			if !ok {
				return nil, fmt.Errorf("key %q not provided by any node", key)
			}
			d.edges[providerID] = append(d.edges[providerID], n.TestID)
			d.inDegree[n.TestID]++
		}
	}

	if err := d.detectCycle(); err != nil {
		return nil, err
	}

	return d, nil
}

// detectCycle runs Kahn's algorithm and returns an error if a cycle is found.
func (d *DAG) detectCycle() error {
	inDeg := make(map[string]int, len(d.inDegree))
	for id, v := range d.inDegree {
		inDeg[id] = v
	}

	queue := make([]string, 0, len(d.nodes))
	for id, deg := range inDeg {
		if deg == 0 {
			queue = append(queue, id)
		}
	}

	processed := 0
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		processed++
		for _, dep := range d.edges[cur] {
			inDeg[dep]--
			if inDeg[dep] == 0 {
				queue = append(queue, dep)
			}
		}
	}

	if processed < len(d.nodes) {
		// Collect the nodes still stuck in the cycle.
		var cycle []string
		for id, deg := range inDeg {
			if deg > 0 {
				cycle = append(cycle, id)
			}
		}
		return fmt.Errorf("cycle detected among nodes: %s", strings.Join(cycle, ", "))
	}
	return nil
}

// ReadyNodes returns all nodes that have no unsatisfied dependencies and are
// still pending (not yet dispatched).
func (d *DAG) ReadyNodes() []*loader.TestNode {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var ready []*loader.TestNode
	for id, deg := range d.inDegree {
		if deg == 0 && d.status[id] == StatusPending {
			ready = append(ready, d.nodes[id])
		}
	}
	return ready
}

// MarkRunning transitions a node to StatusRunning.
func (d *DAG) MarkRunning(testID string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.status[testID] = StatusRunning
}

// MarkPassed transitions a node to StatusPassed, decrements the in-degree of
// its dependants, and returns any nodes that become ready as a result.
func (d *DAG) MarkPassed(testID string) []*loader.TestNode {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.status[testID] = StatusPassed

	var newlyReady []*loader.TestNode
	for _, depID := range d.edges[testID] {
		d.inDegree[depID]--
		if d.inDegree[depID] == 0 && d.status[depID] == StatusPending {
			newlyReady = append(newlyReady, d.nodes[depID])
		}
	}
	return newlyReady
}

// MarkFailed transitions a node to StatusFailed and marks every node reachable
// from it as StatusSkipped: a failed node never wrote its provided keys, so its
// dependants can no longer satisfy their contracts. It returns the skipped IDs.
func (d *DAG) MarkFailed(testID string) []string {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.status[testID] = StatusFailed

	visited := make(map[string]bool)
	queue := []string{testID}
	var skipped []string

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, dep := range d.edges[cur] {
			if visited[dep] {
				continue
			}
			visited[dep] = true
			if d.status[dep] == StatusPending || d.status[dep] == StatusReady {
				d.status[dep] = StatusSkipped
				skipped = append(skipped, dep)
			}
			queue = append(queue, dep)
		}
	}
	return skipped
}

// CancelPending marks every node that has not started as StatusSkipped and
// returns their ids in suite order. It is what closes out a run that stops
// early: nodes still in flight report their own outcome, but the ones that
// never started would otherwise be left with no result at all.
func (d *DAG) CancelPending() []string {
	d.mu.Lock()
	defer d.mu.Unlock()

	var cancelled []string
	for id, s := range d.status {
		if s == StatusPending || s == StatusReady {
			d.status[id] = StatusSkipped
			cancelled = append(cancelled, id)
		}
	}
	sort.Strings(cancelled)
	return cancelled
}

// AllDone reports whether every node has reached a terminal state.
func (d *DAG) AllDone() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()

	for _, s := range d.status {
		if s != StatusPassed && s != StatusFailed && s != StatusSkipped {
			return false
		}
	}
	return true
}

// Summary returns a count of nodes per terminal status label.
func (d *DAG) Summary() map[string]int {
	d.mu.RLock()
	defer d.mu.RUnlock()

	out := map[string]int{"passed": 0, "failed": 0, "skipped": 0}
	for _, s := range d.status {
		switch s {
		case StatusPassed:
			out["passed"]++
		case StatusFailed:
			out["failed"]++
		case StatusSkipped:
			out["skipped"]++
		}
	}
	return out
}

// CriticalPath returns, for each node, the length of the longest chain of
// dependants rooted at that node, counting the node itself. Sinks have length 1.
// The scheduler uses it as a tie-breaker so nodes on longer downstream chains
// start earlier (list-scheduling heuristic, cf. Schmid & Mottok).
func (d *DAG) CriticalPath() map[string]int {
	d.mu.RLock()
	defer d.mu.RUnlock()

	memo := make(map[string]int, len(d.nodes))
	var longest func(id string) int
	longest = func(id string) int {
		if v, ok := memo[id]; ok {
			return v
		}
		best := 0
		for _, dep := range d.edges[id] {
			if c := longest(dep); c > best {
				best = c
			}
		}
		memo[id] = best + 1
		return memo[id]
	}
	for id := range d.nodes {
		longest(id)
	}
	return memo
}

// AllTags returns the sorted set of distinct resource tags declared across the
// suite. It seeds the default agent pool so that every node has a matching agent.
func (d *DAG) AllTags() []string {
	d.mu.RLock()
	defer d.mu.RUnlock()

	seen := make(map[string]bool)
	var tags []string
	for _, n := range d.nodes {
		for _, t := range n.Resources.Tags {
			if !seen[t] {
				seen[t] = true
				tags = append(tags, t)
			}
		}
	}
	sort.Strings(tags)
	return tags
}
