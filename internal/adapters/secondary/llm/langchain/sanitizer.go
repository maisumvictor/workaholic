package langchain

import (
	"regexp"
	"strings"
)

const redacted = "[REDACTED]"

var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
	regexp.MustCompile(`(?i)(aws_secret_access_key|secret[_-]?access[_-]?key)["'\s:=]+[A-Za-z0-9/+=]{40}`),
	regexp.MustCompile(`(?i)aws_session_token["'\s:=]+[A-Za-z0-9/+=]{80,}`),
	regexp.MustCompile(`-----BEGIN (?:RSA |EC |OPENSSH |DSA |ENCRYPTED )?PRIVATE KEY-----[\s\S]+?-----END (?:RSA |EC |OPENSSH |DSA |ENCRYPTED )?PRIVATE KEY-----`),
	regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9\-._~+/]+=*`),
	regexp.MustCompile(`eyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`),
	regexp.MustCompile(`ghp_[A-Za-z0-9]{36}`),
	regexp.MustCompile(`github_pat_[A-Za-z0-9_]{20,}`),
	regexp.MustCompile(`gho_[A-Za-z0-9]{36}`),
	regexp.MustCompile(`xox[baprs]-[A-Za-z0-9-]{10,}`),
	regexp.MustCompile(`sk-(?:proj-)?[A-Za-z0-9]{20,}`),
	regexp.MustCompile(`sk-ant-[A-Za-z0-9\-_]{20,}`),
	regexp.MustCompile(`(?i)(api[_-]?key|authorization)["'\s:=]+[A-Za-z0-9\-._~+/]{16,}`),
}

// Sanitize scrubs credentials and private key material from untrusted telemetry
// before it is allowed to leave the network boundary toward an LLM provider.
func Sanitize(input string) string {
	if input == "" {
		return input
	}
	out := input
	for _, re := range secretPatterns {
		out = re.ReplaceAllString(out, redacted)
	}
	return out
}

// WrapTelemetry isolates untrusted content in delimiters that the system prompt
// instructs the model never to treat as instructions.
func WrapTelemetry(kind, body string) string {
	kind = strings.TrimSpace(kind)
	if kind == "" {
		kind = "data"
	}
	return "<RAW_TELEMETRY kind=\"" + kind + "\">\n" + Sanitize(body) + "\n</RAW_TELEMETRY>"
}
