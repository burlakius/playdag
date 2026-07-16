package loader_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"playdag/internal/loader"
)

func TestLoadSuite(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		path        string
		wantErr     bool
		errContains string
		check       func(t *testing.T, s *loader.Suite)
	}{
		{
			name: "valid suite loads correctly",
			path: "../../testdata/valid_suite.json",
			check: func(t *testing.T, s *loader.Suite) {
				t.Helper()
				if s.SuiteID != "e2e-smoke-v1" {
					t.Errorf("SuiteID = %q, want %q", s.SuiteID, "e2e-smoke-v1")
				}
				if len(s.Nodes) != 3 {
					t.Fatalf("len(Nodes) = %d, want 3", len(s.Nodes))
				}
				ids := []string{"auth.login", "card.create", "card.archive"}
				for i, want := range ids {
					if got := s.Nodes[i].TestID; got != want {
						t.Errorf("Nodes[%d].TestID = %q, want %q", i, got, want)
					}
				}
				if s.Nodes[0].Policy.Criticality != "BLOCKER" {
					t.Errorf("first node criticality = %q, want BLOCKER", s.Nodes[0].Policy.Criticality)
				}
				if s.Nodes[2].Policy.Criticality != "NON_CRITICAL" {
					t.Errorf("third node criticality = %q, want NON_CRITICAL", s.Nodes[2].Policy.Criticality)
				}
			},
		},
		{
			name:        "missing file returns error",
			path:        "../../testdata/nonexistent.json",
			wantErr:     true,
			errContains: "loader: read file:",
		},
		{
			name:        "empty file returns error",
			path:        "../../testdata/empty.json",
			wantErr:     true,
			errContains: "loader: parse JSON:",
		},
		{
			name:        "duplicate test_id returns error",
			path:        "../../testdata/duplicate_ids.json",
			wantErr:     true,
			errContains: "duplicate test_id",
		},
		{
			name:        "invalid criticality returns error",
			path:        "../../testdata/invalid_criticality.json",
			wantErr:     true,
			errContains: "invalid criticality",
		},
		{
			name:        "missing script returns error",
			path:        "../../testdata/missing_script.json",
			wantErr:     true,
			errContains: "script is required",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			suite, err := loader.LoadSuite(tc.path)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tc.errContains != "" && !strings.Contains(err.Error(), tc.errContains) {
					t.Errorf("error = %q, want it to contain %q", err.Error(), tc.errContains)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.check != nil {
				tc.check(t, suite)
			}
		})
	}
}

// writeSuite drops a suite file into a temp dir and returns its path.
func writeSuite(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "suite.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write suite: %v", err)
	}
	return path
}

func TestLoadSuite_InvalidBackoff(t *testing.T) {
	path := writeSuite(t, `{"suite_id":"s","nodes":[{"test_id":"a","script":"a.py",
	  "policy":{"criticality":"BLOCKER","retry_delay":"1s","backoff":"linear"}}]}`)
	_, err := loader.LoadSuite(path)
	if err == nil || !strings.Contains(err.Error(), "backoff") {
		t.Fatalf("expected a backoff error, got: %v", err)
	}
}

// A delay the runner cannot parse would be silently ignored, turning a
// deliberate wait into no wait at all.
func TestLoadSuite_InvalidRetryDelay(t *testing.T) {
	path := writeSuite(t, `{"suite_id":"s","nodes":[{"test_id":"a","script":"a.py",
	  "policy":{"criticality":"BLOCKER","retry_delay":"soon"}}]}`)
	_, err := loader.LoadSuite(path)
	if err == nil || !strings.Contains(err.Error(), "retry_delay") {
		t.Fatalf("expected a retry_delay error, got: %v", err)
	}
}

func TestLoadSuite_BackoffWithoutDelay(t *testing.T) {
	path := writeSuite(t, `{"suite_id":"s","nodes":[{"test_id":"a","script":"a.py",
	  "policy":{"criticality":"BLOCKER","backoff":"exponential"}}]}`)
	_, err := loader.LoadSuite(path)
	if err == nil || !strings.Contains(err.Error(), "retry_delay") {
		t.Fatalf("expected an error about the missing delay, got: %v", err)
	}
}

func TestLoadSuite_ValidRetryPolicy(t *testing.T) {
	path := writeSuite(t, `{"suite_id":"s","nodes":[{"test_id":"a","script":"a.py",
	  "policy":{"criticality":"BLOCKER","retries":2,"retry_delay":"500ms","backoff":"exponential"}}]}`)
	suite, err := loader.LoadSuite(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if suite.Nodes[0].Policy.RetryDelay != "500ms" || suite.Nodes[0].Policy.Backoff != "exponential" {
		t.Fatalf("policy not parsed: %+v", suite.Nodes[0].Policy)
	}
}
