package capture

import (
	"bufio"
	"crypto/sha1"
	"encoding/hex"
	"io"
	"regexp"
	"strings"
)

// CIError is one extracted error signature from a CI log.
type CIError struct {
	Signature string // stable hash for matching
	Title     string // truncated single-line headline
	Snippet   string // up to ~10 lines of surrounding context
	Severity  string // "error" | "panic" | "fail"
}

// errorPattern recognises common CI failure markers across Go, Node, and shell.
// We deliberately stay loose because the goal is recall, not precision —
// downstream callers always run `aria_search` on the signature anyway.
var errorPattern = regexp.MustCompile(
	`(?i)\b(panic:|fatal error:|error[: ]|assertion failed|test failed|FAIL\s|ERROR:|TypeError|SyntaxError|exception:)`,
)

// ghLogLinePrefix strips `2024-04-26T12:34:56.789Z ` timestamps and
// `##[group]` decorations that GitHub Actions emits.
var ghLogLinePrefix = regexp.MustCompile(
	`^(?:\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?Z\s)?(?:##\[[a-z]+\]\s?)?`,
)

// ParseCILog extracts CIError entries from a GitHub Actions log stream.
// Adjacent matches within 5 lines collapse into a single error so we don't
// flood ARIA with duplicate signatures.
func ParseCILog(r io.Reader) []CIError {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	var (
		all     []string
		matches []int
	)
	idx := 0
	for scanner.Scan() {
		line := ghLogLinePrefix.ReplaceAllString(scanner.Text(), "")
		all = append(all, line)
		if errorPattern.MatchString(line) {
			matches = append(matches, idx)
		}
		idx++
	}

	if len(matches) == 0 {
		return nil
	}

	// Group matches that are within 5 lines of each other.
	groups := [][]int{{matches[0]}}
	for _, m := range matches[1:] {
		last := groups[len(groups)-1]
		if m-last[len(last)-1] <= 5 {
			groups[len(groups)-1] = append(last, m)
		} else {
			groups = append(groups, []int{m})
		}
	}

	results := make([]CIError, 0, len(groups))
	for _, g := range groups {
		start := g[0]
		end := g[len(g)-1] + 5
		if end >= len(all) {
			end = len(all) - 1
		}
		snippet := strings.Join(all[start:end+1], "\n")
		title := strings.TrimSpace(all[g[0]])
		if len(title) > 200 {
			title = title[:200] + "..."
		}
		results = append(results, CIError{
			Signature: ErrorSignature(title),
			Title:     title,
			Snippet:   snippet,
			Severity:  classifySeverity(title),
		})
	}
	return results
}

// ErrorSignature produces a stable short hash of the canonicalised error
// title — used as both topic_key and search query.
func ErrorSignature(title string) string {
	canon := canonicaliseError(title)
	sum := sha1.Sum([]byte(canon))
	return "ci-err-" + hex.EncodeToString(sum[:])[:12]
}

// canonicaliseError strips run-specific noise (paths, line numbers, hex IDs)
// so the same logical error produces the same signature across runs.
var (
	rePath   = regexp.MustCompile(`(?:[A-Za-z]:)?(?:/[\w.\-]+)+\.(?:go|ts|js|tsx|jsx|py|rb|sh|java|rs)`)
	reLine   = regexp.MustCompile(`:\d+(:\d+)?`)
	reHex    = regexp.MustCompile(`0x[0-9a-fA-F]+`)
	reUUID   = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
	reNumber = regexp.MustCompile(`\b\d{4,}\b`)
)

func canonicaliseError(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = rePath.ReplaceAllString(s, "<path>")
	s = reLine.ReplaceAllString(s, "")
	s = reHex.ReplaceAllString(s, "<hex>")
	s = reUUID.ReplaceAllString(s, "<uuid>")
	s = reNumber.ReplaceAllString(s, "<n>")
	return strings.Join(strings.Fields(s), " ")
}

func classifySeverity(line string) string {
	low := strings.ToLower(line)
	switch {
	case strings.Contains(low, "panic:"):
		return "panic"
	case strings.Contains(low, "fatal"):
		return "fatal"
	case strings.Contains(low, "fail"):
		return "fail"
	default:
		return "error"
	}
}

// CICaptureRequest is the input to ProcessCIErrors. It mirrors the GitHub
// Actions environment so callers can populate fields directly from
// $GITHUB_* variables.
type CICaptureRequest struct {
	Workflow string
	RunID    string
	Repo     string
	Branch   string
	Commit   string
	LogText  string // raw log content (caller is responsible for fetching)
}

// CICaptureOutcome describes what was done for one parsed error.
type CICaptureOutcome struct {
	Error      CIError
	TopicKey   string
	WouldSave  bool   // true when no prior match was found
	MatchCount int    // number of existing observations matched on signature
	Comment    string // suggested PR/commit comment text
}

// BuildOutcomes produces the per-error CICaptureOutcome list without
// performing any network calls. The caller (cmd/aria-core/main.go) wires
// this up to the actual aria_search / aria_save round-trip.
func BuildOutcomes(req CICaptureRequest, errs []CIError) []CICaptureOutcome {
	out := make([]CICaptureOutcome, 0, len(errs))
	for _, e := range errs {
		out = append(out, CICaptureOutcome{
			Error:    e,
			TopicKey: e.Signature,
			Comment: BuildComment(req, e, 0),
		})
	}
	return out
}

// BuildComment renders the PR/commit comment body for an error. matchCount=0
// means "not seen before" → we phrase it as a fresh capture.
func BuildComment(req CICaptureRequest, e CIError, matchCount int) string {
	var b strings.Builder
	if matchCount > 0 {
		b.WriteString("ARIA encontró ")
		writeInt(&b, matchCount)
		b.WriteString(" observation")
		if matchCount > 1 {
			b.WriteString("es")
		}
		b.WriteString(" sobre este error:\n\n")
	} else {
		b.WriteString("ARIA capturó este error como nueva observación:\n\n")
	}
	b.WriteString("**Workflow:** `")
	b.WriteString(req.Workflow)
	b.WriteString("` — run ")
	b.WriteString(req.RunID)
	b.WriteString("\n")
	b.WriteString("**Signature:** `")
	b.WriteString(e.Signature)
	b.WriteString("`\n\n")
	b.WriteString("```\n")
	b.WriteString(e.Title)
	b.WriteString("\n```\n")
	return b.String()
}

func writeInt(b *strings.Builder, n int) {
	if n == 0 {
		b.WriteByte('0')
		return
	}
	digits := []byte{}
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if neg {
		b.WriteByte('-')
	}
	b.Write(digits)
}
