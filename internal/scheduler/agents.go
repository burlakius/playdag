package scheduler

import (
	"encoding/json"
	"fmt"
	"os"
)

// ParseAgents reads an agent pool definition. spec is either a path to a JSON
// file or an inline JSON array of {"name","tags"} objects. An empty spec yields
// a nil slice, leaving the caller to build a default pool.
func ParseAgents(spec string) ([]*Agent, error) {
	if spec == "" {
		return nil, nil
	}

	data := []byte(spec)
	if b, err := os.ReadFile(spec); err == nil {
		data = b
	}

	var agents []*Agent
	if err := json.Unmarshal(data, &agents); err != nil {
		return nil, fmt.Errorf("scheduler: parse agents: %w", err)
	}

	seen := make(map[string]bool, len(agents))
	for i, a := range agents {
		if a.Name == "" {
			return nil, fmt.Errorf("scheduler: agent[%d] has no name", i)
		}
		if seen[a.Name] {
			return nil, fmt.Errorf("scheduler: duplicate agent name %q", a.Name)
		}
		seen[a.Name] = true
	}
	return agents, nil
}
