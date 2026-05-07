package loader_test

import (
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
