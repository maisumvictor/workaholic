package langchain

import (
	"strings"
	"testing"
)

func TestSanitizeSecrets(t *testing.T) {
	in := "key=AKIAIOSFODNN7EXAMPLE token=Bearer abcdefghijklmnopqrstuvwxyz012345 private=-----BEGIN PRIVATE KEY-----\nMIIB\n-----END PRIVATE KEY----- slack=xoxb-1234567890-abcdefghijk"
	out := Sanitize(in)
	for _, needle := range []string{"AKIAIOSFODNN7EXAMPLE", "Bearer abcdefghijklmnopqrstuvwxyz012345", "BEGIN PRIVATE KEY", "xoxb-1234567890-abcdefghijk"} {
		if strings.Contains(out, needle) {
			t.Errorf("secret %q leaked: %s", needle, out)
		}
	}
	wrapped := WrapTelemetry("logs", in)
	if !strings.Contains(wrapped, "<RAW_TELEMETRY") || !strings.Contains(wrapped, "</RAW_TELEMETRY>") {
		t.Fatalf("missing isolation tags: %s", wrapped)
	}
	if strings.Contains(wrapped, "AKIAIOSFODNN7EXAMPLE") {
		t.Fatal("aws access key leaked through WrapTelemetry")
	}
}
