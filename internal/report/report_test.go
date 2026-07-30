package report

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"playdag/internal/protocol"
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
	// sequential = 10s, actual = 4s -> 2.50x
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

func TestGenerate_FailedNodeListsArtifacts(t *testing.T) {
	r := &Report{
		SuiteID:   "s",
		NodeOrder: []string{"card.create"},
		Results: map[string]runner.AgentResult{
			"card.create": {
				Status:    "FAILED",
				Error:     "timed out",
				Artifacts: []protocol.Artifact{{Type: "trace", Path: "test-results/card/trace.zip"}},
			},
		},
	}

	out := Generate(r)
	if !strings.Contains(out, "test-results/card/trace.zip") {
		t.Fatalf("artifact path missing from the report:\n%s", out)
	}
}

func TestSaveJSON_CarriesArtifacts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.json")
	r := &Report{
		SuiteID:   "s",
		NodeOrder: []string{"card.create"},
		Results: map[string]runner.AgentResult{
			"card.create": {
				Status:    "PASSED",
				Artifacts: []protocol.Artifact{{Type: "video", Path: "test-results/card/video.webm"}},
			},
		},
	}
	if err := SaveJSON(r, path); err != nil {
		t.Fatalf("SaveJSON: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	var got struct {
		Results map[string]struct {
			Artifacts []protocol.Artifact `json:"artifacts"`
		} `json:"results"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("parse report: %v", err)
	}
	arts := got.Results["card.create"].Artifacts
	if len(arts) != 1 || arts[0].Type != "video" || arts[0].Path != "test-results/card/video.webm" {
		t.Fatalf("artifacts lost in the JSON report: %+v", arts)
	}
}

func TestGenerate_FlakyNodeIsCalledOut(t *testing.T) {
	r := &Report{
		SuiteID:   "s",
		NodeOrder: []string{"solid", "flaky"},
		Results: map[string]runner.AgentResult{
			"solid": {Status: "PASSED", Attempts: 1},
			"flaky": {Status: "PASSED", Attempts: 2, Flaky: true},
		},
	}

	out := Generate(r)
	if !strings.Contains(out, "FLAKY") || !strings.Contains(out, "passed on attempt 2") {
		t.Fatalf("the flaky node should be called out:\n%s", out)
	}
	if !strings.Contains(out, "Passed:   2") {
		t.Errorf("a flaky node still passed and must count as passed:\n%s", out)
	}
	if !strings.Contains(out, "Flaky:    1") {
		t.Errorf("the summary should count the flaky node:\n%s", out)
	}
}

func TestSaveJSON_CarriesFlaky(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.json")
	r := &Report{
		SuiteID:   "s",
		NodeOrder: []string{"flaky"},
		Results:   map[string]runner.AgentResult{"flaky": {Status: "PASSED", Attempts: 3, Flaky: true}},
	}
	if err := SaveJSON(r, path); err != nil {
		t.Fatalf("SaveJSON: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	var got struct {
		Flaky   int `json:"flaky"`
		Results map[string]struct {
			Flaky    bool `json:"flaky"`
			Attempts int  `json:"attempts"`
		} `json:"results"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("parse report: %v", err)
	}
	if got.Flaky != 1 || !got.Results["flaky"].Flaky || got.Results["flaky"].Attempts != 3 {
		t.Fatalf("flaky data lost in the JSON report: %+v", got)
	}
}

func TestCosts_Utilization(t *testing.T) {
	// Two agents held open for 10s = 20 agent-seconds of capacity; the nodes used
	// 5s of it.
	r := &Report{
		TotalDuration: 10 * time.Second,
		Workers:       2,
		Results: map[string]runner.AgentResult{
			"a": {Status: "PASSED", Duration: 3 * time.Second},
			"b": {Status: "PASSED", Duration: 2 * time.Second},
		},
	}

	cost := Costs(r)
	if cost.WorkerSeconds != 5 {
		t.Errorf("worker-seconds: want 5, got %v", cost.WorkerSeconds)
	}
	if cost.Utilization != 0.25 {
		t.Errorf("utilization: want 0.25, got %v", cost.Utilization)
	}
	if cost.WallClock != 10*time.Second {
		t.Errorf("wall clock: got %v", cost.WallClock)
	}
}

// A run with no wall clock yet must not divide by zero.
func TestCosts_ZeroDuration(t *testing.T) {
	cost := Costs(&Report{Workers: 4, Results: map[string]runner.AgentResult{}})
	if cost.Utilization != 0 {
		t.Fatalf("want 0, got %v", cost.Utilization)
	}
}

func TestCosts_CountsCachedNodes(t *testing.T) {
	r := &Report{
		TotalDuration: time.Second,
		Workers:       1,
		Results: map[string]runner.AgentResult{
			"ran":    {Status: "PASSED", Duration: time.Second},
			"cached": {Status: "PASSED", Cached: true},
		},
	}
	if got := Costs(r).Cached; got != 1 {
		t.Fatalf("want 1 reused node, got %d", got)
	}
}

func TestGenerate_ShowsCostMetrics(t *testing.T) {
	r := &Report{
		SuiteID:       "s",
		TotalDuration: 10 * time.Second,
		Workers:       2,
		NodeOrder:     []string{"a"},
		Results:       map[string]runner.AgentResult{"a": {Status: "PASSED", Duration: 5 * time.Second}},
	}

	out := Generate(r)
	for _, want := range []string{"Wall clock:", "Worker-seconds:", "Utilization:     25.0%", "2 agent(s)"} {
		if !strings.Contains(out, want) {
			t.Errorf("report should show %q:\n%s", want, out)
		}
	}
}

func TestSaveJSON_CarriesCostMetrics(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.json")
	r := &Report{
		SuiteID:       "s",
		TotalDuration: 10 * time.Second,
		Workers:       2,
		NodeOrder:     []string{"a"},
		Results:       map[string]runner.AgentResult{"a": {Status: "PASSED", Duration: 5 * time.Second}},
	}
	if err := SaveJSON(r, path); err != nil {
		t.Fatalf("SaveJSON: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var got struct {
		Workers       int     `json:"workers"`
		Utilization   float64 `json:"utilization"`
		WorkerSeconds float64 `json:"worker_seconds"`
		// Fields the CI pipeline reads; they must not move.
		TotalTests      int     `json:"total_tests"`
		Passed          int     `json:"passed"`
		TotalDurationMs float64 `json:"total_duration_ms"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.Workers != 2 || got.Utilization != 0.25 || got.WorkerSeconds != 5 {
		t.Errorf("cost metrics: %+v", got)
	}
	if got.TotalTests != 1 || got.Passed != 1 || got.TotalDurationMs != 10000 {
		t.Errorf("the fields CI reads must keep working: %+v", got)
	}
}
