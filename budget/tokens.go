package budget

// EstimateTokens approximates the token count from a byte payload.
// It uses a conservative ~4 characters per token heuristic, which is
// reasonable for JSON payloads where punctuation and short keys dominate.
func EstimateTokens(payload []byte) int {
	n := len(payload)
	if n == 0 {
		return 0
	}
	// Integer ceiling division: (n + 3) / 4
	return (n + 3) / 4
}

// EstimateTokensFromString is a convenience wrapper around [EstimateTokens].
func EstimateTokensFromString(s string) int {
	return EstimateTokens([]byte(s))
}
