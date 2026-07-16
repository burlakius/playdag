package report

import (
	"encoding/xml"
	"fmt"
	"os"
	"time"

	"playdag/internal/runner"
)

// SaveJUnit writes the run as JUnit XML, the format GitLab and Jenkins read
// natively: with it, a failed node shows up on a merge request as a named test
// rather than as a line someone has to find in a log.
func SaveJUnit(r *Report, path string) error {
	b, err := xml.MarshalIndent(junitOf(r), "", "  ")
	if err != nil {
		return fmt.Errorf("report: marshal junit: %w", err)
	}

	out := append([]byte(xml.Header), b...)
	out = append(out, '\n')
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return fmt.Errorf("report: write junit: %w", err)
	}
	return nil
}

// junitSuite is the element consumers actually group by.
type junitSuite struct {
	XMLName   xml.Name    `xml:"testsuite"`
	Name      string      `xml:"name,attr"`
	Tests     int         `xml:"tests,attr"`
	Failures  int         `xml:"failures,attr"`
	Skipped   int         `xml:"skipped,attr"`
	Time      float64     `xml:"time,attr"`
	Timestamp string      `xml:"timestamp,attr"`
	Cases     []junitCase `xml:"testcase"`
}

type junitReport struct {
	XMLName  xml.Name     `xml:"testsuites"`
	Name     string       `xml:"name,attr"`
	Tests    int          `xml:"tests,attr"`
	Failures int          `xml:"failures,attr"`
	Skipped  int          `xml:"skipped,attr"`
	Time     float64      `xml:"time,attr"`
	Suites   []junitSuite `xml:"testsuite"`
}

type junitCase struct {
	Name      string        `xml:"name,attr"`
	ClassName string        `xml:"classname,attr"`
	Time      float64       `xml:"time,attr"`
	Failure   *junitFailure `xml:"failure,omitempty"`
	Skipped   *junitSkipped `xml:"skipped,omitempty"`
	SystemOut string        `xml:"system-out,omitempty"`
}

type junitFailure struct {
	Message string `xml:"message,attr"`
	Type    string `xml:"type,attr"`
	Text    string `xml:",chardata"`
}

type junitSkipped struct {
	Message string `xml:"message,attr"`
}

func junitOf(r *Report) junitReport {
	suite := junitSuite{
		Name:      r.SuiteID,
		Time:      r.TotalDuration.Seconds(),
		Timestamp: r.StartedAt.UTC().Format(time.RFC3339),
	}

	for _, id := range r.NodeOrder {
		res, ok := r.Results[id]
		if !ok {
			continue
		}

		c := junitCase{
			Name:      id,
			ClassName: "playdag." + r.SuiteID,
			Time:      res.Duration.Seconds(),
		}
		switch res.Status {
		case "FAILED":
			c.Failure = &junitFailure{
				Message: firstLine(res.Error),
				Type:    "failure",
				Text:    junitDetail(res),
			}
		case "SKIPPED":
			// Not run is not passed. Reporting it as skipped is what stops a
			// blocked node from being counted as green.
			c.Skipped = &junitSkipped{Message: res.Error}
		default:
			if note := passNote(res); note != "" {
				c.SystemOut = note
			}
		}
		suite.Cases = append(suite.Cases, c)
	}

	suite.Tests = len(suite.Cases)
	for _, c := range suite.Cases {
		if c.Failure != nil {
			suite.Failures++
		}
		if c.Skipped != nil {
			suite.Skipped++
		}
	}

	return junitReport{
		Name:     r.SuiteID,
		Tests:    suite.Tests,
		Failures: suite.Failures,
		Skipped:  suite.Skipped,
		Time:     suite.Time,
		Suites:   []junitSuite{suite},
	}
}

// junitDetail is the body of a failure: the error, plus the artefacts that say
// more about it than any message can.
func junitDetail(res runner.AgentResult) string {
	detail := res.Error
	for _, a := range res.Artifacts {
		detail += "\n" + a.Type + ": " + a.Path
	}
	if res.Attempts > 1 {
		detail += fmt.Sprintf("\nfailed after %d attempts", res.Attempts)
	}
	return detail
}

// passNote records what a green tick would otherwise hide: that the node needed
// a retry, or did not run at all because an earlier run had already done it.
func passNote(res runner.AgentResult) string {
	switch {
	case res.Cached:
		return "reused from a previous run: this node did not execute"
	case res.Flaky:
		return fmt.Sprintf("flaky: passed only on attempt %d", res.Attempts)
	default:
		return ""
	}
}

// firstLine keeps an XML attribute to one line; the full text goes in the body.
func firstLine(s string) string {
	for i, r := range s {
		if r == '\n' {
			return s[:i]
		}
	}
	return s
}
