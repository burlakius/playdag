// Package report generates human-readable and machine-readable summaries of a
// playdag execution run.
package report

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"playdag/internal/runner"
	"playdag/internal/store"
)

// Report holds all data needed to produce execution summaries.
type Report struct {
	SuiteID         string
	StartedAt       time.Time
	FinishedAt      time.Time
	TotalDuration   time.Duration
	Results         map[string]runner.AgentResult
	ContextSnapshot map[string]store.ContextValue
	NodeOrder       []string // original suite order used for display
}

const (
	boxInner     = 54 // inner width of ╔═…═╗ border (columns)
	sectionWidth = 57 // total columns for ── Title ─… lines
)

// Generate returns a formatted text report suitable for printing to stdout.
func Generate(r *Report) string {
	var sb strings.Builder

	border := strings.Repeat("═", boxInner)

	// ── header ──────────────────────────────────────────────
	fmt.Fprintf(&sb, "╔%s╗\n", border)
	fmt.Fprintf(&sb, "║%-*s║\n", boxInner, "         PlayDAG — Execution Report")
	fmt.Fprintf(&sb, "╚%s╝\n", border)

	fmt.Fprintf(&sb, "Suite:     %s\n", r.SuiteID)
	fmt.Fprintf(&sb, "Started:   %s\n", r.StartedAt.Format("2006-01-02 15:04:05"))
	fmt.Fprintf(&sb, "Finished:  %s\n", r.FinishedAt.Format("2006-01-02 15:04:05"))
	fmt.Fprintf(&sb, "Duration:  %.2fs\n", r.TotalDuration.Seconds())

	// ── results ─────────────────────────────────────────────
	sb.WriteString(section("Results"))

	var passed, failed, skipped int
	var seqDuration time.Duration

	for _, id := range r.NodeOrder {
		res, ok := r.Results[id]
		if !ok {
			continue
		}
		switch res.Status {
		case "PASSED":
			passed++
			seqDuration += res.Duration
			fmt.Fprintf(&sb, "✓ PASSED  %-20s %.2fs\n", id, res.Duration.Seconds())
		case "FAILED":
			failed++
			seqDuration += res.Duration
			fmt.Fprintf(&sb, "✗ FAILED  %-20s %s\n", id, res.Error)
		case "SKIPPED":
			skipped++
			fmt.Fprintf(&sb, "↷ SKIPPED %-20s %s\n", id, res.Error)
		}
	}

	// ── summary ─────────────────────────────────────────────
	sb.WriteString(section("Summary"))
	total := passed + failed + skipped
	fmt.Fprintf(&sb, "Total:    %d tests\n", total)
	if total > 0 {
		pct := func(n int) float64 { return 100 * float64(n) / float64(total) }
		fmt.Fprintf(&sb, "Passed:   %d  (%.1f%%)\n", passed, pct(passed))
		fmt.Fprintf(&sb, "Failed:   %d  (%.1f%%)\n", failed, pct(failed))
		fmt.Fprintf(&sb, "Skipped:  %d  (%.1f%%)\n", skipped, pct(skipped))
	}

	// ── context snapshot ─────────────────────────────────────
	sb.WriteString(section("Context Store Snapshot"))
	keys := make([]string, 0, len(r.ContextSnapshot))
	for k := range r.ContextSnapshot {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		cv := r.ContextSnapshot[k]
		fmt.Fprintf(&sb, "%-20s → [provided by %s, scope: %s]\n", k, cv.ProvidedBy, cv.Scope)
	}

	// ── performance ──────────────────────────────────────────
	sb.WriteString(section("Performance"))
	var speedup float64
	if r.TotalDuration > 0 {
		speedup = seqDuration.Seconds() / r.TotalDuration.Seconds()
	}
	fmt.Fprintf(&sb, "Sequential estimate: %.2fs (sum of all durations)\n", seqDuration.Seconds())
	fmt.Fprintf(&sb, "Actual (parallel):   %.2fs\n", r.TotalDuration.Seconds())
	fmt.Fprintf(&sb, "Speedup:             %.2fx\n", speedup)

	// ── footer ───────────────────────────────────────────────
	fmt.Fprintf(&sb, "╔%s╗\n", border)
	if failed > 0 {
		msg := fmt.Sprintf("  Suite FAILED — %d test(s) did not pass", failed)
		fmt.Fprintf(&sb, "║%-*s║\n", boxInner, msg)
	} else {
		fmt.Fprintf(&sb, "║%-*s║\n", boxInner, "  Suite PASSED — all tests green")
	}
	fmt.Fprintf(&sb, "╚%s╝\n", border)

	return sb.String()
}

// section returns a "── Title ────…" header line.
func section(title string) string {
	// "── " is 2 box-drawing chars + space = 3 rune columns; title is ASCII.
	prefixLen := 3 + len(title) + 1
	fill := sectionWidth - prefixLen
	if fill < 0 {
		fill = 0
	}
	return "── " + title + " " + strings.Repeat("─", fill) + "\n"
}

// SaveJSON serialises the report to a JSON file for downstream analysis. The
// top-level aggregate fields (total_tests, passed, worker_seconds, …) are the
// ones consumed by the CI experiment scripts.
func SaveJSON(r *Report, path string) error {
	type nodeResult struct {
		Status     string            `json:"status"`
		DurationMs float64           `json:"duration_ms"`
		Error      string            `json:"error,omitempty"`
		Provides   map[string]string `json:"provides,omitempty"`
	}
	type jsonReport struct {
		SuiteID         string                        `json:"suite_id"`
		StartedAt       time.Time                     `json:"started_at"`
		FinishedAt      time.Time                     `json:"finished_at"`
		TotalDurationMs float64                       `json:"total_duration_ms"`
		TotalTests      int                           `json:"total_tests"`
		Passed          int                           `json:"passed"`
		Failed          int                           `json:"failed"`
		Skipped         int                           `json:"skipped"`
		WorkerSeconds   float64                       `json:"worker_seconds"`
		Results         map[string]nodeResult         `json:"results"`
		ContextSnapshot map[string]store.ContextValue `json:"context_snapshot"`
		NodeOrder       []string                      `json:"node_order"`
	}

	results := make(map[string]nodeResult, len(r.Results))
	var passed, failed, skipped int
	var workerSeconds float64
	for id, res := range r.Results {
		results[id] = nodeResult{
			Status:     res.Status,
			DurationMs: res.Duration.Seconds() * 1000,
			Error:      res.Error,
			Provides:   res.Provides,
		}
		workerSeconds += res.Duration.Seconds()
		switch res.Status {
		case "PASSED":
			passed++
		case "FAILED":
			failed++
		case "SKIPPED":
			skipped++
		}
	}

	payload := jsonReport{
		SuiteID:         r.SuiteID,
		StartedAt:       r.StartedAt,
		FinishedAt:      r.FinishedAt,
		TotalDurationMs: r.TotalDuration.Seconds() * 1000,
		TotalTests:      len(r.NodeOrder),
		Passed:          passed,
		Failed:          failed,
		Skipped:         skipped,
		WorkerSeconds:   workerSeconds,
		Results:         results,
		ContextSnapshot: r.ContextSnapshot,
		NodeOrder:       r.NodeOrder,
	}

	b, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("report: marshal: %w", err)
	}
	return os.WriteFile(path, b, 0o644)
}
