package graph

import (
	"fmt"
	"sort"
	"strings"
)

// dependency is one edge: the key that ties a node to the node providing it.
type dependency struct {
	from string // provider node id
	to   string // dependant node id
	key  string
}

// dependencies lists every edge with the key that caused it, in a stable order.
// The key is the interesting part: it is what makes this a data contract rather
// than a hand-written order.
func (d *DAG) dependencies() []dependency {
	d.mu.RLock()
	defer d.mu.RUnlock()

	providers := make(map[string]string)
	for id, n := range d.nodes {
		for _, key := range n.Contract.Provides {
			providers[key] = id
		}
	}

	var deps []dependency
	for id, n := range d.nodes {
		for _, key := range n.Contract.Requires {
			if from, ok := providers[key]; ok {
				deps = append(deps, dependency{from: from, to: id, key: key})
			}
		}
	}
	sort.Slice(deps, func(i, j int) bool {
		if deps[i].from != deps[j].from {
			return deps[i].from < deps[j].from
		}
		if deps[i].to != deps[j].to {
			return deps[i].to < deps[j].to
		}
		return deps[i].key < deps[j].key
	})
	return deps
}

// sortedIDs returns every node id in a stable order.
func (d *DAG) sortedIDs() []string {
	d.mu.RLock()
	defer d.mu.RUnlock()

	ids := make([]string, 0, len(d.nodes))
	for id := range d.nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// criticalityOf reports whether a node is a blocker.
func (d *DAG) criticalityOf(id string) string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.nodes[id].Policy.Criticality
}

// Mermaid renders the graph as a Mermaid flowchart. Non-critical nodes are drawn
// apart, since their failure does not stop what follows.
func (d *DAG) Mermaid() string {
	var sb strings.Builder
	sb.WriteString("flowchart TD\n")

	var nonCritical []string
	for _, id := range d.sortedIDs() {
		fmt.Fprintf(&sb, "    %s[%q]\n", mermaidID(id), id)
		if d.criticalityOf(id) == "NON_CRITICAL" {
			nonCritical = append(nonCritical, mermaidID(id))
		}
	}

	for _, dep := range d.dependencies() {
		fmt.Fprintf(&sb, "    %s -->|%s| %s\n", mermaidID(dep.from), dep.key, mermaidID(dep.to))
	}

	if len(nonCritical) > 0 {
		sb.WriteString("    classDef nonCritical stroke-dasharray: 4 3\n")
		fmt.Fprintf(&sb, "    class %s nonCritical\n", strings.Join(nonCritical, ","))
	}
	return sb.String()
}

// DOT renders the graph in Graphviz's DOT language.
func (d *DAG) DOT() string {
	var sb strings.Builder
	sb.WriteString("digraph playdag {\n")
	sb.WriteString("    rankdir=LR;\n")
	sb.WriteString("    node [shape=box];\n")

	for _, id := range d.sortedIDs() {
		if d.criticalityOf(id) == "NON_CRITICAL" {
			fmt.Fprintf(&sb, "    %q [style=dashed];\n", id)
			continue
		}
		fmt.Fprintf(&sb, "    %q;\n", id)
	}

	for _, dep := range d.dependencies() {
		fmt.Fprintf(&sb, "    %q -> %q [label=%q];\n", dep.from, dep.to, dep.key)
	}

	sb.WriteString("}\n")
	return sb.String()
}

// mermaidID makes a node id usable as a Mermaid identifier, which may not carry
// the dots test ids are written with.
func mermaidID(id string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		default:
			return '_'
		}
	}, id)
}
