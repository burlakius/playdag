package report

import (
	"fmt"
	"html/template"
	"math"
	"os"
	"sort"
	"time"

	"playdag/internal/runner"
)

// SaveHTML writes a single-file HTML report to path: one document with its CSS
// inlined and no external assets, so it can be opened straight from a CI
// artefact archive.
func SaveHTML(r *Report, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("report: create html: %w", err)
	}
	defer f.Close()

	if err := htmlTemplate.Execute(f, buildView(r)); err != nil {
		return fmt.Errorf("report: render html: %w", err)
	}
	return nil
}

// htmlView is the report shaped for the template: every number is already
// computed here, so the template only places values.
type htmlView struct {
	SuiteID       string
	StartedAt     string
	FinishedAt    string
	WallClock     string
	WorkerSeconds string
	Utilization   string
	Workers       int
	Total         int
	Passed        int
	Failed        int
	Skipped       int
	Flaky         int
	Cached        int
	Green         bool
	Lanes         []lane
	Ticks         []tick
	Nodes         []nodeRow
	Context       []ctxRow
}

// lane is one agent's row on the timeline. Gaps between its bars are idle time.
type lane struct {
	Agent string
	Bars  []bar
}

// bar is a node's span, positioned as a percentage of the run's wall clock.
type bar struct {
	ID       string
	Status   string
	Title    string
	LeftPct  float64
	WidthPct float64
}

type tick struct {
	LeftPct float64
	Label   string
}

type nodeRow struct {
	ID        string
	Status    string
	Label     string // what the row is called: PASSED, FLAKY, CACHED, …
	Duration  string
	Attempts  int
	Agent     string
	Error     string
	Artifacts []string
}

type ctxRow struct {
	Key        string
	ProvidedBy string
	Scope      string
	Value      string
}

// minBarPct keeps a very short node visible: a bar of 0.05% is a bar nobody can
// see, and an invisible node reads as a node that never ran.
const minBarPct = 0.4

func buildView(r *Report) htmlView {
	cost := Costs(r)
	v := htmlView{
		SuiteID:       r.SuiteID,
		StartedAt:     r.StartedAt.Format("2006-01-02 15:04:05"),
		FinishedAt:    r.FinishedAt.Format("2006-01-02 15:04:05"),
		WallClock:     fmt.Sprintf("%.2fs", cost.WallClock.Seconds()),
		WorkerSeconds: fmt.Sprintf("%.2fs", cost.WorkerSeconds),
		Utilization:   fmt.Sprintf("%.1f%%", 100*cost.Utilization),
		Workers:       r.Workers,
		Cached:        cost.Cached,
	}

	for _, id := range r.NodeOrder {
		res, ok := r.Results[id]
		if !ok {
			continue
		}
		v.Total++
		switch res.Status {
		case "PASSED":
			v.Passed++
		case "FAILED":
			v.Failed++
		case "SKIPPED":
			v.Skipped++
		}
		if res.Flaky {
			v.Flaky++
		}
		v.Nodes = append(v.Nodes, row(id, res))
	}
	v.Green = v.Failed == 0

	v.Lanes = buildLanes(r)
	v.Ticks = buildTicks(r.TotalDuration)
	v.Context = buildContext(r)
	return v
}

// statusLabel is what a node is called in the report. The status alone would
// hide that a node needed a retry, or that it never ran at all.
func statusLabel(res runner.AgentResult) string {
	switch {
	case res.Cached:
		return "CACHED"
	case res.Flaky:
		return "FLAKY"
	default:
		return res.Status
	}
}

// statusClass is the CSS class carrying the same distinction as a colour.
func statusClass(res runner.AgentResult) string {
	switch {
	case res.Cached:
		return "cached"
	case res.Flaky:
		return "flaky"
	case res.Status == "PASSED":
		return "passed"
	case res.Status == "FAILED":
		return "failed"
	default:
		return "skipped"
	}
}

func row(id string, res runner.AgentResult) nodeRow {
	out := nodeRow{
		ID:       id,
		Status:   statusClass(res),
		Label:    statusLabel(res),
		Duration: fmt.Sprintf("%.2fs", res.Duration.Seconds()),
		Attempts: res.Attempts,
		Agent:    res.Agent,
		Error:    res.Error,
	}
	for _, a := range res.Artifacts {
		out.Artifacts = append(out.Artifacts, a.Type+": "+a.Path)
	}
	return out
}

// buildLanes puts each node on the row of the agent that ran it. Lanes make the
// shape of the run visible: bars side by side are parallelism, and the gaps are
// agents waiting on the graph.
func buildLanes(r *Report) []lane {
	total := r.TotalDuration.Seconds()
	if total <= 0 {
		return nil
	}

	byAgent := make(map[string][]bar)
	for id, res := range r.Results {
		if res.StartedAt.IsZero() || res.Agent == "" {
			continue // never ran: it has no place on a timeline
		}
		left := 100 * res.StartedAt.Sub(r.StartedAt).Seconds() / total
		width := 100 * res.Duration.Seconds() / total
		if width < minBarPct {
			width = minBarPct
		}
		byAgent[res.Agent] = append(byAgent[res.Agent], bar{
			ID:       id,
			Status:   statusClass(res),
			Title:    fmt.Sprintf("%s — %s, %.2fs on %s", id, statusLabel(res), res.Duration.Seconds(), res.Agent),
			LeftPct:  round2(clamp(left)),
			WidthPct: round2(width),
		})
	}

	agents := make([]string, 0, len(byAgent))
	for a := range byAgent {
		agents = append(agents, a)
	}
	sort.Strings(agents)

	lanes := make([]lane, 0, len(agents))
	for _, a := range agents {
		bars := byAgent[a]
		sort.Slice(bars, func(i, j int) bool { return bars[i].LeftPct < bars[j].LeftPct })
		lanes = append(lanes, lane{Agent: a, Bars: bars})
	}
	return lanes
}

// buildTicks places a few labelled gridlines, so a bar's length can be read as
// seconds rather than guessed.
func buildTicks(total time.Duration) []tick {
	if total <= 0 {
		return nil
	}
	const steps = 4
	ticks := make([]tick, 0, steps+1)
	for i := 0; i <= steps; i++ {
		frac := float64(i) / steps
		ticks = append(ticks, tick{
			LeftPct: 100 * frac,
			Label:   fmt.Sprintf("%.1fs", total.Seconds()*frac),
		})
	}
	return ticks
}

func buildContext(r *Report) []ctxRow {
	keys := make([]string, 0, len(r.ContextSnapshot))
	for k := range r.ContextSnapshot {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	rows := make([]ctxRow, 0, len(keys))
	for _, k := range keys {
		cv := r.ContextSnapshot[k]
		rows = append(rows, ctxRow{Key: k, ProvidedBy: cv.ProvidedBy, Scope: cv.Scope, Value: cv.Value})
	}
	return rows
}

// round2 keeps the generated markup readable: nobody needs a bar positioned to
// the fourteenth decimal.
func round2(pct float64) float64 {
	return math.Round(pct*100) / 100
}

func clamp(pct float64) float64 {
	if pct < 0 {
		return 0
	}
	if pct > 100 {
		return 100
	}
	return pct
}

var htmlTemplate = template.Must(template.New("report").Parse(`<!doctype html>
<html lang="en">
<meta charset="utf-8">
<title>playdag — {{.SuiteID}}</title>
<style>
  :root {
    --bg: #ffffff; --fg: #1c1e21; --muted: #6b7280; --line: #e5e7eb;
    --passed: #16a34a; --failed: #dc2626; --skipped: #9ca3af;
    --flaky: #d97706; --cached: #2563eb;
  }
  @media (prefers-color-scheme: dark) {
    :root { --bg: #111317; --fg: #e5e7eb; --muted: #9ca3af; --line: #2b2f36; }
  }
  * { box-sizing: border-box; }
  body { margin: 0; padding: 2rem 1.5rem; background: var(--bg); color: var(--fg);
         font: 15px/1.5 -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; }
  main { max-width: 1100px; margin: 0 auto; }
  h1 { font-size: 1.5rem; margin: 0 0 .25rem; }
  h2 { font-size: 1.05rem; margin: 2.5rem 0 .75rem; }
  .sub { color: var(--muted); margin: 0 0 1.5rem; font-size: .9rem; }
  .verdict { display: inline-block; padding: .15rem .6rem; border-radius: 999px;
             font-size: .8rem; font-weight: 600; color: #fff; }
  .verdict.green { background: var(--passed); }
  .verdict.red { background: var(--failed); }
  .cards { display: grid; grid-template-columns: repeat(auto-fit, minmax(140px, 1fr)); gap: .75rem; }
  .card { border: 1px solid var(--line); border-radius: 8px; padding: .75rem .9rem; }
  .card .n { font-size: 1.5rem; font-weight: 600; }
  .card .k { color: var(--muted); font-size: .8rem; }
  .lane { display: grid; grid-template-columns: 90px 1fr; align-items: center; gap: .5rem; margin-bottom: .35rem; }
  .lane .name { color: var(--muted); font-size: .8rem; text-align: right;
                overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .track { position: relative; height: 26px; background: var(--line); border-radius: 4px; overflow: hidden; }
  .bar { position: absolute; top: 3px; height: 20px; border-radius: 3px; min-width: 2px;
         color: #fff; font-size: .7rem; line-height: 20px; padding: 0 .3rem;
         overflow: hidden; white-space: nowrap; }
  .bar.passed { background: var(--passed); }
  .bar.failed { background: var(--failed); }
  .bar.skipped { background: var(--skipped); }
  .bar.flaky { background: var(--flaky); }
  .bar.cached { background: var(--cached); }
  .axis { position: relative; height: 1.2rem; margin-left: 98px; color: var(--muted); font-size: .7rem; }
  .axis span { position: absolute; transform: translateX(-50%); }
  table { width: 100%; border-collapse: collapse; font-size: .875rem; }
  th, td { text-align: left; padding: .45rem .5rem; border-bottom: 1px solid var(--line); vertical-align: top; }
  th { color: var(--muted); font-weight: 600; font-size: .78rem; text-transform: uppercase; letter-spacing: .03em; }
  td.num { text-align: right; font-variant-numeric: tabular-nums; }
  .tag { font-size: .72rem; font-weight: 600; padding: .1rem .4rem; border-radius: 4px; color: #fff; }
  .tag.passed { background: var(--passed); } .tag.failed { background: var(--failed); }
  .tag.skipped { background: var(--skipped); } .tag.flaky { background: var(--flaky); }
  .tag.cached { background: var(--cached); }
  code { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: .85em; }
  .err { color: var(--failed); }
  .empty { color: var(--muted); font-style: italic; }
</style>
<main>
  <h1>{{.SuiteID}}
    {{if .Green}}<span class="verdict green">PASSED</span>
    {{else}}<span class="verdict red">FAILED</span>{{end}}
  </h1>
  <p class="sub">{{.StartedAt}} → {{.FinishedAt}}</p>

  <div class="cards">
    <div class="card"><div class="n">{{.Total}}</div><div class="k">nodes</div></div>
    <div class="card"><div class="n">{{.Passed}}</div><div class="k">passed</div></div>
    <div class="card"><div class="n">{{.Failed}}</div><div class="k">failed</div></div>
    <div class="card"><div class="n">{{.Skipped}}</div><div class="k">skipped</div></div>
    {{if .Flaky}}<div class="card"><div class="n">{{.Flaky}}</div><div class="k">flaky</div></div>{{end}}
    {{if .Cached}}<div class="card"><div class="n">{{.Cached}}</div><div class="k">reused</div></div>{{end}}
    <div class="card"><div class="n">{{.WallClock}}</div><div class="k">wall clock</div></div>
    <div class="card"><div class="n">{{.WorkerSeconds}}</div><div class="k">worker-seconds</div></div>
    <div class="card"><div class="n">{{.Utilization}}</div><div class="k">utilization of {{.Workers}} agent(s)</div></div>
  </div>

  <h2>Timeline</h2>
  {{if .Lanes}}
    {{range .Lanes}}
    <div class="lane">
      <div class="name" title="{{.Agent}}">{{.Agent}}</div>
      <div class="track">
        {{range .Bars}}
        <div class="bar {{.Status}}" style="left:{{.LeftPct}}%;width:{{.WidthPct}}%" title="{{.Title}}">{{.ID}}</div>
        {{end}}
      </div>
    </div>
    {{end}}
    <div class="axis">{{range .Ticks}}<span style="left:{{.LeftPct}}%">{{.Label}}</span>{{end}}</div>
  {{else}}
    <p class="empty">No node ran long enough to place on a timeline.</p>
  {{end}}

  <h2>Nodes</h2>
  <table>
    <tr><th>Node</th><th>Status</th><th class="num">Duration</th><th class="num">Attempts</th><th>Agent</th><th>Detail</th></tr>
    {{range .Nodes}}
    <tr>
      <td><code>{{.ID}}</code></td>
      <td><span class="tag {{.Status}}">{{.Label}}</span></td>
      <td class="num">{{.Duration}}</td>
      <td class="num">{{if .Attempts}}{{.Attempts}}{{end}}</td>
      <td>{{.Agent}}</td>
      <td>
        {{if .Error}}<span class="err">{{.Error}}</span>{{end}}
        {{range .Artifacts}}<div><code>{{.}}</code></div>{{end}}
      </td>
    </tr>
    {{end}}
  </table>

  <h2>Context</h2>
  {{if .Context}}
  <table>
    <tr><th>Key</th><th>Provided by</th><th>Scope</th><th>Value</th></tr>
    {{range .Context}}
    <tr>
      <td><code>{{.Key}}</code></td>
      <td><code>{{.ProvidedBy}}</code></td>
      <td>{{.Scope}}</td>
      <td><code>{{.Value}}</code></td>
    </tr>
    {{end}}
  </table>
  {{else}}<p class="empty">No values were shared between nodes.</p>{{end}}
</main>
</html>
`))
