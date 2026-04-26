package mcp

import (
	"strings"
	"testing"
)

func TestMaskValue_ReplacesAllOccurrences(t *testing.T) {
	out := maskValue("got DB_PASS=supersecret123 more text supersecret123 end", "DB_PASS", "supersecret123")
	if strings.Contains(out, "supersecret123") {
		t.Errorf("expected value masked, got: %s", out)
	}
	if strings.Count(out, "<<MASKED:DB_PASS>>") != 2 {
		t.Errorf("expected 2 mask occurrences, got: %s", out)
	}
}

func TestMaskValue_SkipsShortValues(t *testing.T) {
	out := maskValue("normal text with abc inside", "X", "abc")
	if !strings.Contains(out, "abc") {
		t.Errorf("short value should not be masked (false positives), got: %s", out)
	}
}

func TestMaskValue_PreservesNonMatchingText(t *testing.T) {
	out := maskValue("the quick brown fox", "TOKEN", "longenoughvalue")
	if out != "the quick brown fox" {
		t.Errorf("non-matching text should be unchanged, got: %s", out)
	}
}

func TestCommandIsSafe_AllowsBenignCommands(t *testing.T) {
	allowed := []string{
		"echo hello",
		"psql -h $DB_HOST -U postgres -c 'SELECT 1'",
		"curl https://api.example.com -H \"Authorization: Bearer $API_KEY\"",
		"ls -la /tmp",
		"rm /tmp/somefile",       // local rm OK
		"chmod 600 /tmp/key.pem", // safe chmod
	}
	for _, c := range allowed {
		if !commandIsSafe(c) {
			t.Errorf("expected safe: %q", c)
		}
	}
}

func TestCommandIsSafe_BlocksDestructive(t *testing.T) {
	blocked := []string{
		"rm -rf /",
		"rm -rf /var",
		"rm -rf ~",
		"sudo rm -rf /",
		"dd if=/dev/zero of=/dev/sda",
		"mkfs.ext4 /dev/sda1",
		"shutdown -h now",
		"reboot now",
		":(){ :|:& };:",
		"chmod -R 777 /",
		"echo abc > /dev/sda",
	}
	for _, c := range blocked {
		if commandIsSafe(c) {
			t.Errorf("expected blocked: %q", c)
		}
	}
}

func TestSha256Hex(t *testing.T) {
	h := sha256Hex("hello")
	want := "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if h != want {
		t.Errorf("sha256(hello) = %s, want %s", h, want)
	}
}
