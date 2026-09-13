package urlutil

import "testing"

func TestValidateWebhookURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		// Valid
		{"empty is allowed", "", false},
		{"valid https", "https://example.com/webhook", false},
		{"valid http", "http://example.com/webhook", false},
		{"valid with port", "https://example.com:8443/hook", false},

		// Blocked schemes
		{"file scheme", "file:///etc/passwd", true},
		{"ftp scheme", "ftp://example.com", true},

		// Blocked hostnames
		{"localhost", "http://localhost/hook", true},
		{"metadata aws", "http://169.254.169.254/latest/meta-data", true},
		{"google metadata", "http://metadata.google.internal/v1/metadata", true},
		{"dot local", "http://myhost.local/hook", true},
		{"dot internal", "http://service.internal/hook", true},
		{"dot localhost", "http://sub.localhost/hook", true},

		// Invalid URLs
		{"no scheme", "example.com/webhook", true},
		{"garbage", "not-a-url", true},
		{"no host", "https:///path", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateWebhookURL(tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateWebhookURL(%q) error = %v, wantErr %v", tt.url, err, tt.wantErr)
			}
		})
	}
}
