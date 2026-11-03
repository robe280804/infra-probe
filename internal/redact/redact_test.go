package redact

import "testing"

func TestString(t *testing.T) {
	tests := []struct{ in, want string }{
		{"mysql --password=s3cret -h db", "mysql --password=<redacted> -h db"},
		{"redis://:pw@cache:6379", "redis://:<redacted>@cache:6379"},
		{"https://user:pw@example.com/x", "https://user:<redacted>@example.com/x"},
		{"secret-app:1.0", "secret-app:1.0"},
		{"https://example.com:8443/path", "https://example.com:8443/path"},
	}
	for _, tt := range tests {
		if got := String(tt.in); got != tt.want {
			t.Errorf("String(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestReference(t *testing.T) {
	tests := []struct {
		in         string
		wantMasked string
		wantOnly   bool
	}{
		{"${DB_PASSWORD}", "${DB_PASSWORD}", true},
		{"$DB_PASSWORD", "$DB_PASSWORD", true},
		{"${HOST}:${PORT}", "${HOST}:${PORT}", false}, // text between references is a literal
		{"${A}${B}", "${A}${B}", true},
		{"${DB_PASS:-hunter2}", "${DB_PASS:-<redacted>}", true},
		{"${DB_PASS:?must be set}", "${DB_PASS:?<redacted>}", true},
		{"hunter2", "hunter2", false},
		{"prefix-${A}", "prefix-${A}", false},
		{"", "", false},
	}
	for _, tt := range tests {
		masked, only := Reference(tt.in)
		if masked != tt.wantMasked || only != tt.wantOnly {
			t.Errorf("Reference(%q) = (%q, %v), want (%q, %v)", tt.in, masked, only, tt.wantMasked, tt.wantOnly)
		}
	}
}
