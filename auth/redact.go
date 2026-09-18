package auth

// RedactedValue is the placeholder [Redact] substitutes for a credential
// value. The key survives so a caller (typically a UI) can show that a
// header exists without exposing what it is, and a value coming back in as
// exactly RedactedValue means "leave it alone" — see [MergeRedacted].
const RedactedValue = "••••••••"

// Redact returns a copy of headers with every non-empty value replaced by
// RedactedValue. It is for config that is credential-shaped by
// construction — HTTP headers configured for an outbound MCP server
// connection, where any of them may carry a bearer token or API key — not a
// general-purpose secret scanner. An empty value is left empty rather than
// redacted, since there is nothing to hide and redacting it would falsely
// suggest a credential is configured.
func Redact(headers map[string]string) map[string]string {
	out := make(map[string]string, len(headers))
	for k, v := range headers {
		if v == "" {
			out[k] = v
			continue
		}
		out[k] = RedactedValue
	}
	return out
}

// MergeRedacted merges incoming over stored, except that a key whose
// incoming value is exactly RedactedValue keeps its stored value instead.
// This is the write-side counterpart to [Redact]: a caller that reads a
// redacted config back and submits it unmodified (echoing RedactedValue for
// every credential) does not overwrite the real value with the placeholder.
// A key present in incoming but absent from stored is dropped when its
// value is RedactedValue, since there is no stored value to preserve.
func MergeRedacted(incoming, stored map[string]string) map[string]string {
	out := make(map[string]string, len(incoming))
	for k, v := range incoming {
		if v == RedactedValue {
			if old, ok := stored[k]; ok {
				out[k] = old
			}
			continue
		}
		out[k] = v
	}
	return out
}
