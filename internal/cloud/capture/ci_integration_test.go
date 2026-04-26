package capture

import (
	"strings"
	"testing"
)

func TestParseCILog_BasicErrorDetected(t *testing.T) {
	log := `2024-04-26T12:34:56.789Z ##[group]Run go test ./...
2024-04-26T12:34:57.123Z --- FAIL: TestFoo (0.01s)
2024-04-26T12:34:57.123Z     foo_test.go:42: expected 1, got 2
2024-04-26T12:34:57.124Z FAIL    github.com/x/y/foo  0.020s
2024-04-26T12:34:57.124Z FAIL
`
	errs := ParseCILog(strings.NewReader(log))
	if len(errs) == 0 {
		t.Fatal("expected at least one error")
	}
	if errs[0].Signature == "" {
		t.Fatal("signature must not be empty")
	}
}

func TestParseCILog_GroupsCloseMatches(t *testing.T) {
	log := `error: first failure
error: second failure same area
unrelated line one
unrelated line two
unrelated line three
unrelated line four
unrelated line five
unrelated line six
unrelated line seven
error: distant failure
`
	errs := ParseCILog(strings.NewReader(log))
	if len(errs) != 2 {
		t.Fatalf("want 2 grouped errors, got %d: %+v", len(errs), errs)
	}
}

func TestErrorSignature_StableAcrossPathNoise(t *testing.T) {
	a := ErrorSignature("error: foo failed at /home/runner/work/repo/main.go:42:9")
	b := ErrorSignature("error: foo failed at /Users/dev/proj/main.go:101:3")
	if a != b {
		t.Fatalf("signatures should match across path/line noise: %s vs %s", a, b)
	}
}

func TestErrorSignature_DifferentForDifferentErrors(t *testing.T) {
	a := ErrorSignature("error: nil pointer")
	b := ErrorSignature("error: index out of range")
	if a == b {
		t.Fatal("distinct errors must have distinct signatures")
	}
}

func TestCanonicaliseError_StripsHexAndUUID(t *testing.T) {
	got := canonicaliseError("panic: 0xdeadbeef on session 6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	if strings.Contains(got, "deadbeef") {
		t.Fatalf("hex not stripped: %s", got)
	}
	if strings.Contains(got, "6ba7b810") {
		t.Fatalf("uuid not stripped: %s", got)
	}
}

func TestBuildComment_NoMatch(t *testing.T) {
	req := CICaptureRequest{Workflow: "ci.yml", RunID: "123", Repo: "x/y"}
	e := CIError{Signature: "ci-err-abc", Title: "panic: nil pointer"}
	got := BuildComment(req, e, 0)
	if !strings.Contains(got, "nueva observación") {
		t.Fatalf("expected new-capture phrasing, got: %s", got)
	}
}

func TestBuildComment_WithMatches(t *testing.T) {
	req := CICaptureRequest{Workflow: "ci.yml", RunID: "123"}
	e := CIError{Signature: "ci-err-abc", Title: "panic: nil pointer"}
	got := BuildComment(req, e, 3)
	if !strings.Contains(got, "ARIA encontró 3 observation") {
		t.Fatalf("expected match count phrasing, got: %s", got)
	}
}

func TestBuildOutcomes_OneEntryPerError(t *testing.T) {
	errs := []CIError{
		{Signature: "ci-err-1", Title: "a"},
		{Signature: "ci-err-2", Title: "b"},
	}
	out := BuildOutcomes(CICaptureRequest{}, errs)
	if len(out) != 2 {
		t.Fatalf("want 2 outcomes, got %d", len(out))
	}
	if out[0].TopicKey != "ci-err-1" {
		t.Fatalf("topic key mismatch: %s", out[0].TopicKey)
	}
}
