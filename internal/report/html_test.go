package report

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"playdag/internal/protocol"
	"playdag/internal/runner"
	"playdag/internal/store"
)

// timelineReport is a run whose shape is worth drawing: a root node alone, then
// two nodes in parallel on other agents, plus a node that never ran.
func timelineReport() *Report {
	start := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	at := func(offset time.Duration) time.Time { return start.Add(offset) }

	return &Report{
		SuiteID:       "demo",
		StartedAt:     start,
		FinishedAt:    at(10 * time.Second),
		TotalDuration: 10 * time.Second,
		Workers:       2,
		NodeOrder:     []string{"auth.login", "card.create", "card.move", "never.ran"},
		Results: map[string]runner.AgentResult{
			"auth.login": {
				Status: "PASSED", Agent: "agent-1", Attempts: 1,
				StartedAt: start, FinishedAt: at(5 * time.Second), Duration: 5 * time.Second,
			},
			"card.create": {
				Status: "PASSED", Agent: "agent-1", Attempts: 2, Flaky: true,
				StartedAt: at(5 * time.Second), FinishedAt: at(8 * time.Second), Duration: 3 * time.Second,
			},
			"card.move": {
				Status: "FAILED", Agent: "agent-2", Attempts: 1, Error: "expect(received).toBe(expected)",
				StartedAt: at(5 * time.Second), FinishedAt: at(7 * time.Second), Duration: 2 * time.Second,
				Artifacts: []protocol.Artifact{{Type: "trace", Path: "test-results/trace.zip"}},
			},
			"never.ran": {Status: "SKIPPED", Error: "blocked by: card.move"},
		},
		ContextSnapshot: map[string]store.ContextValue{
			"session.token": {Value: "tok123", ProvidedBy: "auth.login", Scope: "suite"},
		},
	}
}

func renderHTML(t *testing.T, r *Report) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "report.html")
	if err := SaveHTML(r, path); err != nil {
		t.Fatalf("SaveHTML: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return string(data)
}

// The report is opened straight from a CI artefact archive, where nothing else
// is fetchable: every asset must already be in the file.
func TestSaveHTML_IsSelfContained(t *testing.T) {
	out := renderHTML(t, timelineReport())

	for _, forbidden := range []string{"<script", "src=", "href=", "@import", "url("} {
		if strings.Contains(out, forbidden) {
			t.Errorf("the report must not reference anything external, found %q", forbidden)
		}
	}
	if !strings.Contains(out, "<style>") {
		t.Error("the CSS should be inlined")
	}
}

// A bar's position is the whole point: it must match when the node actually ran.
func TestSaveHTML_BarsArePlacedOnTheTimeline(t *testing.T) {
	out := renderHTML(t, timelineReport())

	// auth.login: starts at 0s of 10s, runs 5s -> left 0%, width 50%.
	if !strings.Contains(out, `style="left:0%;width:50%"`) {
		t.Errorf("auth.login should span the first half:\n%s", out)
	}
	// card.create: starts at 5s, runs 3s -> left 50%, width 30%.
	if !strings.Contains(out, `style="left:50%;width:30%"`) {
		t.Errorf("card.create should start halfway and run 30%%:\n%s", out)
	}
}

// Lanes are per agent, which is what makes parallelism and idle time visible.
func TestSaveHTML_LanesAreGroupedByAgent(t *testing.T) {
	out := renderHTML(t, timelineReport())

	for _, agent := range []string{"agent-1", "agent-2"} {
		if !strings.Contains(out, `title="`+agent+`"`) {
			t.Errorf("expected a lane for %s:\n%s", agent, out)
		}
	}
	if got := strings.Count(out, `class="lane"`); got != 2 {
		t.Errorf("want 2 lanes, got %d", got)
	}
}

// A node that never ran has no span, so it belongs in the table, not on the
// timeline where it would draw a bar at time zero.
func TestSaveHTML_NodesThatNeverRanAreNotOnTheTimeline(t *testing.T) {
	out := renderHTML(t, timelineReport())

	timeline := out[strings.Index(out, "<h2>Timeline</h2>"):strings.Index(out, "<h2>Nodes</h2>")]
	if strings.Contains(timeline, "never.ran") {
		t.Errorf("a node that never ran must not have a bar:\n%s", timeline)
	}
	if !strings.Contains(out, "never.ran") {
		t.Error("it must still be reported in the table")
	}
}

func TestSaveHTML_StatusesAreDistinguished(t *testing.T) {
	out := renderHTML(t, timelineReport())

	for _, want := range []string{"bar passed", "bar failed", "bar flaky"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected a %q bar:\n%s", want, out)
		}
	}
	// A flaky node is passed, but must not be shown as a plain pass.
	if !strings.Contains(out, ">FLAKY<") {
		t.Error("the flaky node should be labelled as such")
	}
}

func TestSaveHTML_ShowsCostAndVerdict(t *testing.T) {
	out := renderHTML(t, timelineReport())

	for _, want := range []string{"10.00s", "wall clock", "worker-seconds", "50.0%", "verdict red"} {
		if !strings.Contains(out, want) {
			t.Errorf("report should contain %q", want)
		}
	}
}

func TestSaveHTML_GreenRun(t *testing.T) {
	r := timelineReport()
	r.Results["card.move"] = runner.AgentResult{Status: "PASSED", Agent: "agent-2", Duration: time.Second}
	r.Results["never.ran"] = runner.AgentResult{Status: "PASSED", Agent: "agent-2", Duration: time.Second}

	out := renderHTML(t, r)
	if !strings.Contains(out, "verdict green") {
		t.Error("a run with no failures should be green")
	}
}

// The report shows agent output and test errors; neither may become markup.
func TestSaveHTML_EscapesContent(t *testing.T) {
	r := timelineReport()
	r.Results["card.move"] = runner.AgentResult{
		Status: "FAILED", Agent: "agent-2", Duration: time.Second,
		StartedAt: r.StartedAt, FinishedAt: r.StartedAt.Add(time.Second),
		Error: `<script>alert("xss")</script>`,
	}

	out := renderHTML(t, r)
	if strings.Contains(out, "<script>alert") {
		t.Fatalf("agent output must not be able to inject markup:\n%s", out)
	}
	if !strings.Contains(out, "&lt;script&gt;") {
		t.Error("the error text should still be shown, escaped")
	}
}

// A run that produced nothing must still render rather than divide by zero.
func TestSaveHTML_EmptyRun(t *testing.T) {
	out := renderHTML(t, &Report{SuiteID: "empty", Results: map[string]runner.AgentResult{}})
	if !strings.Contains(out, "empty") {
		t.Error("the suite id should still be shown")
	}
}
