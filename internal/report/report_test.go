package report

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"playdag/internal/runner"
	"playdag/internal/store"
)

var t0 = time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)

func baseReport() *Report {
	return &Report{
		SuiteID:         "test_suite",
		StartedAt:       t0,
		FinishedAt:      t0.Add(4 * time.Second),
		TotalDuration:   4 * time.Second,
		Results:         map[string]runner.AgentResult{},
		ContextSnapshot: map[string]store.ContextValue{},
		NodeOrder:       []string{},
	}
}

func TestGenerate_AllPassed_Footer(t *testing.T) {
	r := baseReport()
	r.Results = map[string]runner.AgentResult{
		"a": {Status: "PASSED", Duration: 1 * time.Second},
		"b": {Status: "PASSED", Duration: 1 * time.Second},
	}
	r.NodeOrder = []string{"a", "b"}

	out := Generate(r)
	if !strings.Contains(out, "Suite PASSED") {
		t.Errorf("expected 'Suite PASSED' in output, got:\n%s", out)
	}
	if strings.Contains(out, "Suite FAILED") {
		t.Errorf("unexpected 'Suite FAILED' in output")
	}
}

func TestGenerate_AnyFailed_Footer(t *testing.T) {
	r := baseReport()
	r.Results = map[string]runner.AgentResult{
		"a": {Status: "PASSED", Duration: 1 * time.Second},
		"b": {Status: "FAILED", Error: "timeout"},
	}
	r.NodeOrder = []string{"a", "b"}

	out := Generate(r)
	if !strings.Contains(out, "Suite FAILED") {
		t.Errorf("expected 'Suite FAILED' in output, got:\n%s", out)
	}
	if strings.Contains(out, "Suite PASSED") {
		t.Errorf("unexpected 'Suite PASSED' in output")
	}
}

func TestGenerate_SpeedupCalculation(t *testing.T) {
	r := baseReport()
	// sequential = 10s, actual = 4s → 2.50x
	r.Results = map[string]runner.AgentResult{
		"a": {Status: "PASSED", Duration: 6 * time.Second},
		"b": {Status: "PASSED", Duration: 4 * time.Second},
	}
	r.NodeOrder = []string{"a", "b"}

	out := Generate(r)
	if !strings.Contains(out, "2.50x") {
		t.Errorf("expected speedup 2.50x, got:\n%s", out)
	}
	if !strings.Contains(out, "10.00s") {
		t.Errorf("expected sequential estimate 10.00s, got:\n%s", out)
	}
}

func TestSaveJSON_AggregateFields(t *testing.T) {
	r := baseReport()
	r.Results = map[string]runner.AgentResult{
		"a": {Status: "PASSED", Duration: 2 * time.Second, Provides: map[string]string{"a.out": "1"}},
		"b": {Status: "FAILED", Duration: 1 * time.Second, Error: "boom"},
		"c": {Status: "SKIPPED", Error: "blocked by: b"},
	}
	r.NodeOrder = []string{"a", "b", "c"}

	path := filepath.Join(t.TempDir(), "report.json")
	if err := SaveJSON(r, path); err != nil {
		t.Fatalf("SaveJSON: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}

	var got struct {
		TotalTests    int     `json:"total_tests"`
		Passed        int     `json:"passed"`
		Failed        int     `json:"failed"`
		Skipped       int     `json:"skipped"`
		WorkerSeconds float64 `json:"worker_seconds"`
		Results       map[string]struct {
			DurationMs float64 `json:"duration_ms"`
		} `json:"results"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.TotalTests != 3 || got.Passed != 1 || got.Failed != 1 || got.Skipped != 1 {
		t.Fatalf("unexpected counts: %+v", got)
	}
	if got.WorkerSeconds != 3 {
		t.Errorf("worker_seconds: want 3, got %v", got.WorkerSeconds)
	}
	if got.Results["a"].DurationMs != 2000 {
		t.Errorf("a duration_ms: want 2000, got %v", got.Results["a"].DurationMs)
	}
}

func TestGenerate_SkippedShowsBlockedBy(t *testing.T) {
	r := baseReport()
	r.Results = map[string]runner.AgentResult{
		"auth.login":  {Status: "FAILED", Error: "timeout"},
		"card.create": {Status: "SKIPPED", Error: "blocked by: auth.login"},
	}
	r.NodeOrder = []string{"auth.login", "card.create"}

	out := Generate(r)
	if !strings.Contains(out, "↷ SKIPPED") {
		t.Errorf("expected skipped marker, got:\n%s", out)
	}
	if !strings.Contains(out, "blocked by: auth.login") {
		t.Errorf("expected 'blocked by: auth.login' in output, got:\n%s", out)
	}
}
