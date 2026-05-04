package doctor

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestAggregateStatus(t *testing.T) {
	cases := []struct {
		name     string
		statuses []Status
		want     Status
	}{
		{"all ok", []Status{StatusOK, StatusOK}, StatusOK},
		{"info doesn't escalate", []Status{StatusOK, StatusInfo}, StatusOK},
		{"warn beats ok", []Status{StatusOK, StatusWarn}, StatusWarn},
		{"error beats warn", []Status{StatusWarn, StatusError, StatusOK}, StatusError},
		{"empty", []Status{}, StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			checks := make([]Check, len(tc.statuses))
			for i, s := range tc.statuses {
				checks[i] = Check{Status: s}
			}
			got := aggregateStatus(checks)
			if got != tc.want {
				t.Errorf("aggregateStatus(%v) = %v, want %v", tc.statuses, got, tc.want)
			}
		})
	}
}

func TestRunCheckRecoversFromPanic(t *testing.T) {
	c := runCheck("panicker", func() (Status, string) {
		panic("boom")
	})
	if c.Status != StatusError {
		t.Errorf("expected error status from panicking check, got %v", c.Status)
	}
	if !strings.Contains(c.Detail, "panic") {
		t.Errorf("expected panic mention in detail, got %q", c.Detail)
	}
}

func TestReportSortedByName(t *testing.T) {
	r := Report{
		Checks: []Check{
			{Name: "z_check"},
			{Name: "a_check"},
			{Name: "m_check"},
		},
	}
	sorted := r.SortedByName()
	if sorted.Checks[0].Name != "a_check" || sorted.Checks[2].Name != "z_check" {
		t.Errorf("not sorted: %v", sorted.Checks)
	}
	// Original should not be mutated
	if r.Checks[0].Name != "z_check" {
		t.Errorf("original mutated: %v", r.Checks)
	}
}

func TestReportJSONShape(t *testing.T) {
	r := Report{
		Timestamp:     time.Now().UTC(),
		OverallStatus: StatusOK,
		Version:       "v0.2.0-test",
		Checks: []Check{
			{Name: "config_dir", Status: StatusOK, Detail: "ok", DurationMs: 1},
		},
	}
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	str := string(data)
	for _, want := range []string{`"status":"ok"`, `"version":"v0.2.0-test"`, `"name":"config_dir"`} {
		if !strings.Contains(str, want) {
			t.Errorf("JSON missing %q: %s", want, str)
		}
	}
}

func TestHumanSize(t *testing.T) {
	cases := map[int64]string{
		512:                "512 B",
		1024:               "1.0 KB",
		1024 * 1024:        "1.0 MB",
		1024 * 1024 * 1024: "1.0 GB",
	}
	for in, want := range cases {
		got := humanSize(in)
		if got != want {
			t.Errorf("humanSize(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestHumanizeAge(t *testing.T) {
	cases := map[time.Duration]string{
		30 * time.Second: "30s",
		5 * time.Minute:  "5m",
		2 * time.Hour:    "2h",
		3 * 24 * time.Hour: "3d",
	}
	for in, want := range cases {
		got := humanizeAge(in)
		if got != want {
			t.Errorf("humanizeAge(%v) = %q, want %q", in, got, want)
		}
	}
}
