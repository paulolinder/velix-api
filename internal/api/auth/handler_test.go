package auth

import (
	"net/mail"
	"testing"
)

// TestSlugRegexp validates the compiled slugRegexp against known valid/invalid slugs.
func TestSlugRegexp_Valid(t *testing.T) {
	valid := []string{
		"my-workspace",
		"abc",
		"test-workspace-123",
		"a1b2c3",
		"workspace",
		"hello-world",
	}
	for _, slug := range valid {
		if !slugRegexp.MatchString(slug) {
			t.Errorf("slug %q should be valid but was rejected", slug)
		}
	}
}

func TestSlugRegexp_Invalid(t *testing.T) {
	invalid := []string{
		"",                // empty
		"ab",             // too short (needs 3+ chars)
		"-leading",       // leading hyphen
		"trailing-",      // trailing hyphen
		"UPPERCASE",      // uppercase letters
		"has space",      // space
		"has/slash",      // path separator
		"has.dot",        // dot
		"has_underscore", // underscore
		"--double-dash",  // leading double-dash
	}
	for _, slug := range invalid {
		if slugRegexp.MatchString(slug) {
			t.Errorf("slug %q should be invalid but was accepted", slug)
		}
	}
}

// TestEmailParsing verifies that net/mail.ParseAddress behaves as expected
// for the email validation we added to the Register handler.
func TestEmailParsing_Valid(t *testing.T) {
	valid := []string{
		"user@example.com",
		"admin@domain.org",
		"test+tag@subdomain.io",
		"a@b.co",
	}
	for _, e := range valid {
		if _, err := mail.ParseAddress(e); err != nil {
			t.Errorf("email %q should be valid: %v", e, err)
		}
	}
}

func TestEmailParsing_Invalid(t *testing.T) {
	invalid := []string{
		"notanemail",
		"missing-at-sign",
		"@nodomain",
		"no.domain",
		"double@@sign.com",
	}
	for _, e := range invalid {
		if _, err := mail.ParseAddress(e); err == nil {
			t.Errorf("email %q should be invalid but was accepted", e)
		}
	}
}
