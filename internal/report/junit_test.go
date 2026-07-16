package report

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func renderJUnit(t *testing.T, r *Report) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "junit.xml")
	if err := SaveJUnit(r, path); err != nil {
		t.Fatalf("SaveJUnit: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return string(data)
}

// parsed is the shape a CI consumer sees, decoded back from the file.
type parsedJUnit struct {
	XMLName  xml.Name `xml:"testsuites"`
	Name     string   `xml:"name,attr"`
	Tests    int      `xml:"tests,attr"`
	Failures int      `xml:"failures,attr"`
	Skipped  int      `xml:"skipped,attr"`
	Suites   []struct {
		Name      string `xml:"name,attr"`
		Tests     int    `xml:"tests,attr"`
		Timestamp string `xml:"timestamp,attr"`
		Cases     []struct {
			Name      string  `xml:"name,attr"`
			ClassName string  `xml:"classname,attr"`
			Time      float64 `xml:"time,attr"`
			Failure   *struct {
				Message string `xml:"message,attr"`
				Text    string `xml:",chardata"`
			} `xml:"failure"`
			Skipped *struct {
				Message string `xml:"message,attr"`
			} `xml:"skipped"`
			SystemOut string `xml:"system-out"`
		} `xml:"testcase"`
	} `xml:"testsuite"`
}

func parseJUnit(t *testing.T, r *Report) parsedJUnit {
	t.Helper()
	var out parsedJUnit
	if err := xml.Unmarshal([]byte(renderJUnit(t, r)), &out); err != nil {
		t.Fatalf("the file must parse as XML, or no CI can read it: %v", err)
	}
	return out
}

func TestSaveJUnit_ReportsEveryNode(t *testing.T) {
	got := parseJUnit(t, timelineReport())

	if got.Tests != 4 || got.Failures != 1 || got.Skipped != 1 {
		t.Fatalf("counts: tests=%d failures=%d skipped=%d", got.Tests, got.Failures, got.Skipped)
	}
	if len(got.Suites) != 1 || len(got.Suites[0].Cases) != 4 {
		t.Fatalf("want one suite of 4 cases, got %+v", got.Suites)
	}
	if got.Suites[0].Timestamp == "" {
		t.Error("the suite should carry a timestamp")
	}
}

// GitLab groups by classname and names the test by name: both must be there or
// a failure shows up on a merge request as an unnamed row.
func TestSaveJUnit_CaseIdentity(t *testing.T) {
	got := parseJUnit(t, timelineReport())

	c := got.Suites[0].Cases[0]
	if c.Name != "auth.login" || c.ClassName != "playdag.demo" {
		t.Fatalf("case identity: name=%q classname=%q", c.Name, c.ClassName)
	}
	if c.Time != 5 {
		t.Errorf("time: want 5s, got %v", c.Time)
	}
}

func TestSaveJUnit_FailureCarriesTheDetail(t *testing.T) {
	got := parseJUnit(t, timelineReport())

	for _, c := range got.Suites[0].Cases {
		if c.Name != "card.move" {
			continue
		}
		if c.Failure == nil {
			t.Fatal("card.move should be a failure")
		}
		if !strings.Contains(c.Failure.Message, "toBe") {
			t.Errorf("message: %q", c.Failure.Message)
		}
		// The trace is what someone opens next; it must survive into the body.
		if !strings.Contains(c.Failure.Text, "test-results/trace.zip") {
			t.Errorf("the artefact should be in the failure body: %q", c.Failure.Text)
		}
		return
	}
	t.Fatal("card.move missing from the report")
}

// A blocked node is not a passing node.
func TestSaveJUnit_SkippedIsNotPassed(t *testing.T) {
	got := parseJUnit(t, timelineReport())

	for _, c := range got.Suites[0].Cases {
		if c.Name != "never.ran" {
			continue
		}
		if c.Skipped == nil {
			t.Fatal("a node that never ran must be reported as skipped")
		}
		if !strings.Contains(c.Skipped.Message, "blocked by") {
			t.Errorf("the reason should survive: %q", c.Skipped.Message)
		}
		return
	}
	t.Fatal("never.ran missing from the report")
}

// JUnit has no standard for flaky or cached, so the fact goes where a human
// reading the CI report will still see it rather than being dropped.
func TestSaveJUnit_FlakyIsNoted(t *testing.T) {
	got := parseJUnit(t, timelineReport())

	for _, c := range got.Suites[0].Cases {
		if c.Name != "card.create" {
			continue
		}
		if c.Failure != nil {
			t.Fatal("a flaky node passed; it must not be reported as a failure")
		}
		if !strings.Contains(c.SystemOut, "flaky") {
			t.Errorf("the retry should be noted: %q", c.SystemOut)
		}
		return
	}
	t.Fatal("card.create missing from the report")
}

func TestSaveJUnit_CachedIsNoted(t *testing.T) {
	r := timelineReport()
	res := r.Results["auth.login"]
	res.Cached = true
	r.Results["auth.login"] = res

	got := parseJUnit(t, r)
	if !strings.Contains(got.Suites[0].Cases[0].SystemOut, "did not execute") {
		t.Errorf("a cached node should say it did not run: %q", got.Suites[0].Cases[0].SystemOut)
	}
}

func TestSaveJUnit_HasXMLHeader(t *testing.T) {
	if out := renderJUnit(t, timelineReport()); !strings.HasPrefix(out, "<?xml") {
		t.Fatalf("the file should start with an XML declaration, got: %.20s", out)
	}
}
