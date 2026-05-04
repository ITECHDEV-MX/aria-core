package redactor

import "testing"

func TestRFCMatch(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"ABCD123456XYZ", true},  // 13 chars, persona física
		{"ABC123456XYZ", true},   // 12 chars, persona moral
		{"ABCD123456X1Z", true},  // homoclave puede tener dígitos
		{"ABCDEF123456XYZ", false}, // 15 chars
		{"AB123456XYZ", false},   // 11 chars
	}
	for _, c := range cases {
		got := reRFC.MatchString(c.in)
		if got != c.want {
			t.Errorf("reRFC(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestCURPMatch(t *testing.T) {
	good := []string{
		"BADD110313HCMLNS09", // ejemplo SAT
		"GAJC850920HNLRRR03",
	}
	bad := []string{
		"BADD110313HCMLNS0",   // 17 chars
		"badd110313hcmlns090", // demasiado largo
		"1234567890123456",    // no formato
	}
	for _, c := range good {
		if !reCURP.MatchString(c) {
			t.Errorf("reCURP(%q) should match", c)
		}
	}
	for _, c := range bad {
		if reCURP.MatchString(c) {
			t.Errorf("reCURP(%q) should NOT match", c)
		}
	}
}

func TestEmailMatch(t *testing.T) {
	good := []string{"jc@itechdev.com.mx", "contacto+ventas@parkinc.com", "user.name@example.co"}
	for _, c := range good {
		if !reEmail.MatchString(c) {
			t.Errorf("reEmail(%q) should match", c)
		}
	}
	if reEmail.MatchString("no-email-here") {
		t.Errorf("reEmail should not match plain text")
	}
}

func TestPhoneMatch(t *testing.T) {
	good := []string{
		"+52 81 1234 5678",
		"(81) 1234-5678",
		"8112345678",
		"81-1234-5678",
	}
	for _, c := range good {
		matches := rePhone.FindAllString(c, -1)
		if len(matches) == 0 || !phoneLooksReal(matches[0]) {
			t.Errorf("phone %q should match (got %v)", c, matches)
		}
	}
	if phoneLooksReal("0000000000") {
		t.Errorf("phoneLooksReal should reject all-zeros")
	}
}

func TestAmountMatch(t *testing.T) {
	good := []string{
		"$1,250,000.00 MXN",
		"$25,000",
		"$ 100.50 USD",
		"$2,500 pesos",
	}
	for _, c := range good {
		if !reAmount.MatchString(c) {
			t.Errorf("reAmount(%q) should match", c)
		}
	}
}

func TestLegalNameMatch(t *testing.T) {
	good := []string{
		"Park Inc S.A. de C.V.",
		"Servicios MX, S. de R.L.",
		"Fundación XYZ A.C.",
		"Grupo SAPI de C.V.",
	}
	for _, c := range good {
		if !reLegalName.MatchString(c) {
			t.Errorf("reLegalName(%q) should match", c)
		}
	}
}

func TestCLABEMatch(t *testing.T) {
	if !reCLABE.MatchString("012180001234567890") {
		t.Errorf("CLABE 18-digit should match")
	}
	if reCLABE.MatchString("123") {
		t.Errorf("CLABE should not match 3-digit")
	}
}

func TestLuhnValid(t *testing.T) {
	cards := map[string]bool{
		"4242 4242 4242 4242": true, // Stripe test card
		"4111111111111111":    true, // Visa test
		"1234567812345670":    true, // valid Luhn
		"1234567812345678":    false,
	}
	for c, want := range cards {
		got := luhnValid(c)
		if got != want {
			t.Errorf("luhnValid(%q) = %v, want %v", c, got, want)
		}
	}
}

func TestIPv4Match(t *testing.T) {
	good := []string{"192.168.1.1", "10.0.0.5", "255.255.255.255", "127.0.0.1"}
	bad := []string{"256.1.1.1", "1.2.3.4567", "999.999.999.999", "not.an.ip.addr"}

	for _, c := range good {
		if !reIPv4.MatchString(c) {
			t.Errorf("reIPv4(%q) should match", c)
			continue
		}
		if !ipv4LooksReal(c) {
			t.Errorf("ipv4LooksReal(%q) should be true", c)
		}
	}
	for _, c := range bad {
		if reIPv4.MatchString(c) && ipv4LooksReal(c) {
			t.Errorf("reIPv4+ipv4LooksReal(%q) should reject", c)
		}
	}
}

func TestIPv6Match(t *testing.T) {
	good := []string{
		"2001:0db8:85a3:0000:0000:8a2e:0370:7334",
		"fe80::1ff:fe23:4567:890a",
		"::1",
	}
	for _, c := range good {
		if !reIPv6.MatchString(c) {
			t.Errorf("reIPv6(%q) should match", c)
		}
	}
}

func TestSSNMatch(t *testing.T) {
	good := []string{"123-45-6789", "987-65-4321"}
	bad := []string{"12-34-5678", "1234567890", "abc-de-fghi"}
	for _, c := range good {
		if !reSSN.MatchString(c) {
			t.Errorf("reSSN(%q) should match", c)
		}
	}
	for _, c := range bad {
		if reSSN.MatchString(c) {
			t.Errorf("reSSN(%q) should NOT match", c)
		}
	}
}

func TestAPIKeyMatch(t *testing.T) {
	good := []string{
		"sk-abc123def456ghi789jkl0",                     // OpenAI-shape
		"sk-ant-api03-AbCdEf1234567890XyZ",              // Anthropic-shape
		"ghp_aBcDeFgHiJkLmNoPqRsTuVwXyZ123456",          // GitHub PAT
		"ghs_aBcDeFgHiJkLmNoPqRsTuVwXyZ123456",          // GitHub server token
		"xoxb-1234567890-AbCdEfGhIjKl",                  // Slack bot token
		"AKIAIOSFODNN7EXAMPLE",                          // AWS access key
		"sk_live_aBcDeFgHiJkLmNoPqRsTu",                 // Stripe live secret
		"pk_test_aBcDeFgHiJkLmNoPqRsTu",                 // Stripe test publishable
	}
	bad := []string{
		"sk-too-short",                  // < 16 chars after prefix
		"abc123def456",                  // no recognized prefix
		"my regular text here",
	}
	for _, c := range good {
		if !reAPIKey.MatchString(c) {
			t.Errorf("reAPIKey(%q) should match", c)
		}
	}
	for _, c := range bad {
		if reAPIKey.MatchString(c) {
			t.Errorf("reAPIKey(%q) should NOT match", c)
		}
	}
}

// TestBuiltinPatternsRegistersNewTypes makes sure the new pattern
// types appear in the master list returned by builtinPatterns().
func TestBuiltinPatternsRegistersNewTypes(t *testing.T) {
	have := map[PatternType]bool{}
	for _, p := range builtinPatterns() {
		have[p.Type] = true
	}
	for _, want := range []PatternType{PatternIPv4, PatternIPv6, PatternSSN, PatternAPIKey, PatternCreditCard} {
		if !have[want] {
			t.Errorf("builtinPatterns() missing %q", want)
		}
	}
}
