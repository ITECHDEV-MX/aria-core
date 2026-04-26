package email

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestSendRawHTMLNotConfiguredReturnsError verifies that the Service refuses
// to send when M365 is not configured — protecting against accidental sends in
// dev environments.
func TestSendRawHTMLNotConfiguredReturnsError(t *testing.T) {
	svc := NewService(nil)
	err := svc.SendRawHTML(context.Background(), "client@example.com", nil, "Subject", "<p>Body</p>")
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("expected ErrNotConfigured, got %v", err)
	}
}

// TestSendRawHTMLValidatesInputs verifies that the Service rejects empty
// recipients/subject/body BEFORE making any network call.
func TestSendRawHTMLValidatesInputs(t *testing.T) {
	c, _ := NewClient(Config{
		TenantID: "t", ClientID: "c", ClientSecret: "s", FromAddress: "from@example.com",
	})
	svc := NewService(c)

	cases := []struct {
		name          string
		to, subject, body string
		wantErrSubstr string
	}{
		{"empty to", "", "Subject", "<p>x</p>", "recipient is required"},
		{"empty subject", "to@x.com", "", "<p>x</p>", "subject is required"},
		{"empty body", "to@x.com", "Subject", "", "body is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := svc.SendRawHTML(context.Background(), tc.to, nil, tc.subject, tc.body)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tc.wantErrSubstr) {
				t.Errorf("err = %q, want substring %q", err.Error(), tc.wantErrSubstr)
			}
		})
	}
}
