package sanitize

import (
	"encoding/json"
	"regexp"
	"strings"
)

// Report describes what the sanitizer changed in a single Sanitize call.
type Report struct {
	// FieldsCleaned is the set of param keys whose value was modified
	// (markup stripped, tags array recovered, etc.). Each key appears at
	// most once.
	FieldsCleaned []string
	// RecoveredFields maps OTHER → CONTENT for sibling parameters that
	// were extracted from a leaked <parameter name="OTHER">…</parameter>
	// block AND injected into the args map because the existing slot was
	// empty/whitespace. If args[OTHER] already had a non-empty value, the
	// markup is dropped without recovery.
	RecoveredFields map[string]string
	// DroppedFragments lists each markup fragment that was stripped from
	// a value, in the order encountered. Useful for diagnostics.
	DroppedFragments []string
}

// Changed reports whether anything was modified.
func (r Report) Changed() bool {
	return len(r.FieldsCleaned) > 0 ||
		len(r.RecoveredFields) > 0 ||
		len(r.DroppedFragments) > 0
}

// Sanitize normalizes free-text params in a tool-call args map. It returns a
// cloned map; the original is untouched. Safe to call on nil/empty input.
//
// Sanitize applies the four detection patterns documented in the package
// README, in order, per field. It does not log; callers that want telemetry
// should use [Middleware] or check report.Changed() themselves.
func Sanitize(args map[string]any) (cleaned map[string]any, report Report) {
	report.RecoveredFields = map[string]string{}

	if len(args) == 0 {
		// Always return a non-nil cloned map so callers can use it
		// uniformly. nil input → nil-equivalent empty result.
		return map[string]any{}, report
	}

	// Shallow-clone the input so the caller's map is untouched. Values are
	// copied by reference; we replace the slots we mutate.
	cleaned = make(map[string]any, len(args))
	for k, v := range args {
		cleaned[k] = v
	}

	// Track which keys are non-empty strings up front, so a sibling
	// recovered later doesn't overwrite a value that was already present
	// at call time.
	originallyHadValue := map[string]bool{}
	for k, v := range cleaned {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			originallyHadValue[k] = true
		}
	}

	// Pass 1: every string-typed field gets pattern 1-3 treatment.
	for key, val := range cleaned {
		s, ok := val.(string)
		if !ok {
			continue
		}
		clean, recovered, changed := CleanFreeText(s, key)
		if !changed {
			continue
		}
		cleaned[key] = clean
		report.FieldsCleaned = append(report.FieldsCleaned, key)

		// Distribute recovered siblings.
		for otherKey, otherVal := range recovered {
			report.DroppedFragments = append(report.DroppedFragments,
				renderSiblingFragment(otherKey, otherVal))
			if originallyHadValue[otherKey] {
				// Sibling already has a clean value — drop the
				// markup but do NOT overwrite.
				continue
			}
			cleaned[otherKey] = otherVal
			report.RecoveredFields[otherKey] = otherVal
		}

		// Anything trimmed that wasn't a sibling block is also a
		// dropped fragment (length difference). We log it as a
		// generic fragment for visibility.
		trimmedLen := len(s) - len(clean)
		extractedLen := 0
		for _, v := range recovered {
			extractedLen += len(v)
		}
		if trimmedLen > 0 && extractedLen == 0 {
			report.DroppedFragments = append(report.DroppedFragments,
				s[len(clean):])
		}
	}

	// Pattern 4: tags-array recovery. Runs after pass 1 so a "tags" field
	// that was a string-with-leakage gets pattern 1-3 first, then the
	// JSON-array unmarshal attempt.
	if rawTags, ok := cleaned["tags"]; ok {
		switch v := rawTags.(type) {
		case []any:
			// Already an array — leave alone.
		case string:
			arr, ok := tryUnmarshalStringArray(v)
			if ok {
				cleaned["tags"] = arr
				if !contains(report.FieldsCleaned, "tags") {
					report.FieldsCleaned = append(report.FieldsCleaned, "tags")
				}
			} else {
				// Drop it — set to empty array.
				cleaned["tags"] = []any{}
				if !contains(report.FieldsCleaned, "tags") {
					report.FieldsCleaned = append(report.FieldsCleaned, "tags")
				}
				if v != "" {
					report.DroppedFragments = append(report.DroppedFragments, v)
				}
			}
		default:
			_ = v
		}
	}

	return cleaned, report
}

// CleanFreeText is the per-field workhorse for callers that don't want the
// whole-args wrapper. paramName is the JSON key of this field — used to
// detect a self-named close-tag literal at the value's tail.
//
// Returns the cleaned string, a map of OTHER→CONTENT for any sibling
// <parameter> blocks extracted from the value, and a bool indicating whether
// anything was changed.
func CleanFreeText(value, paramName string) (clean string, recovered map[string]string, changed bool) {
	clean = value
	recovered = map[string]string{}

	// Pattern 2 (run before pattern 1 so we don't strip a self-close-tag
	// that's actually the marker introducing a sibling block).
	cleanedAfterSiblings, sibs := extractSiblingParameters(clean)
	if len(sibs) > 0 {
		clean = cleanedAfterSiblings
		for k, v := range sibs {
			recovered[k] = v
		}
		changed = true
	}

	// Pattern 1: trailing self-named close tag for this field.
	if paramName != "" {
		marker := "</" + paramName + ">"
		if idx := strings.LastIndex(clean, marker); idx >= 0 {
			tail := clean[idx+len(marker):]
			if isTrailingNoise(tail) {
				clean = clean[:idx]
				changed = true
			}
		}
	}

	// Pattern 3: trailing generic close tag (any name) near the tail.
	if newClean, did := stripTrailingGenericCloseTag(clean); did {
		clean = newClean
		changed = true
	}

	// Trim any trailing whitespace/newlines we exposed by stripping
	// markup. Only do this when we already changed something — otherwise
	// we'd erase legitimate trailing newlines on clean input.
	if changed {
		trimmed := strings.TrimRight(clean, " \t\r\n")
		if trimmed != clean {
			clean = trimmed
		}
	}

	return clean, recovered, changed
}

// renderSiblingFragment reconstructs a printable form of a stripped sibling
// block for diagnostic logging. We don't try to be byte-perfect — it's a
// human-readable stand-in.
func renderSiblingFragment(name, content string) string {
	return `<parameter name="` + name + `">` + content + `</parameter>`
}

// extractSiblingParameters finds all `<parameter name="X">CONTENT</parameter>`
// blocks (and the open-only tail variant) and returns the cleaned string plus
// a map of name→content. The cleaned string has the markup removed.
//
// Implementation note: we walk the string left-to-right, capturing each
// `<parameter name="…">` open-tag and its matching close-tag (or end-of-input
// for the open-only variant). Nested `<parameter>` is not supported (the
// pollution shape never produces it).
func extractSiblingParameters(s string) (clean string, recovered map[string]string) {
	recovered = map[string]string{}
	var out strings.Builder
	out.Grow(len(s))

	i := 0
	for i < len(s) {
		// Find next open-tag.
		j := strings.Index(s[i:], `<parameter name="`)
		if j < 0 {
			out.WriteString(s[i:])
			break
		}
		openStart := i + j

		// Locate end of name attribute.
		nameStart := openStart + len(`<parameter name="`)
		nameEnd := strings.Index(s[nameStart:], `"`)
		if nameEnd < 0 {
			// Malformed, give up — copy rest and exit.
			out.WriteString(s[i:])
			break
		}
		name := s[nameStart : nameStart+nameEnd]

		// Find closing `>` of open-tag.
		gtStart := nameStart + nameEnd
		gt := strings.Index(s[gtStart:], `>`)
		if gt < 0 {
			out.WriteString(s[i:])
			break
		}
		contentStart := gtStart + gt + 1

		// Look for matching `</parameter>` close-tag.
		closeIdx := strings.Index(s[contentStart:], `</parameter>`)
		var content string
		var blockEnd int
		if closeIdx < 0 {
			// Open-only variant: content runs to EOF.
			content = s[contentStart:]
			blockEnd = len(s)
		} else {
			content = s[contentStart : contentStart+closeIdx]
			blockEnd = contentStart + closeIdx + len(`</parameter>`)
		}

		// Emit everything before the open-tag.
		out.WriteString(s[i:openStart])
		// Strip any whitespace separator immediately preceding the
		// markup so we don't leave dangling newlines behind.
		trimSeparatorTail(&out)

		// Record the recovered field. If the same name appears twice
		// (rare), last write wins.
		if name != "" {
			recovered[name] = strings.TrimSpace(content)
		}

		i = blockEnd
		// Consume any whitespace/newlines immediately after the block.
		for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\r' || s[i] == '\n') {
			i++
		}
	}

	clean = out.String()
	if len(recovered) == 0 {
		// Nothing extracted — return empty map signal so caller
		// short-circuits the changed=true branch.
		return s, map[string]string{}
	}
	return clean, recovered
}

// trimSeparatorTail removes trailing whitespace/newlines from the buffer so
// stripped markup doesn't leave a dangling blank line at the end of the
// preceding content.
func trimSeparatorTail(b *strings.Builder) {
	cur := b.String()
	trimmed := strings.TrimRight(cur, " \t\r\n")
	if trimmed != cur {
		b.Reset()
		b.WriteString(trimmed)
	}
}

// isTrailingNoise reports whether the string after a self-named close-tag is
// just whitespace, empty, or short markup leakage we should treat as drop-able.
func isTrailingNoise(tail string) bool {
	t := strings.TrimSpace(tail)
	if t == "" {
		return true
	}
	// If the tail itself opens another <parameter> block we already
	// handled it in pattern 2 — but a stray `<parameter` without proper
	// content can still be here. Treat short markup-ish tails as noise.
	if strings.HasPrefix(t, "<") && len(t) < 256 {
		return true
	}
	return false
}

// genericCloseTagAtEnd matches a trailing `</xxx>` followed by optional
// whitespace at end of string.
var genericCloseTagAtEnd = regexp.MustCompile(`</[A-Za-z][A-Za-z0-9_:-]*>\s*$`)

// stripTrailingGenericCloseTag implements pattern 3: a trailing `</xxx>` that
// looks like leakage. Heuristic: the close-tag is at end-of-string (after
// stripping trailing whitespace) AND the whole string isn't an HTML/Markdown
// document with a matching open-tag. We accept both criteria as "leakage" if
// there's no balancing open-tag earlier in the string.
func stripTrailingGenericCloseTag(s string) (clean string, changed bool) {
	loc := genericCloseTagAtEnd.FindStringIndex(s)
	if loc == nil {
		return s, false
	}
	tag := s[loc[0]:loc[1]]
	// Extract just the name to check for a matching open-tag.
	name := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(tag), "</"), ">")
	openMarker := "<" + name
	if idx := strings.Index(s[:loc[0]], openMarker); idx >= 0 {
		// There's a matching open-tag earlier — could be legitimate
		// HTML/Markdown. Only strip if it looks like a torn fragment
		// (open-tag is < 64 chars from the close).
		if loc[0]-idx < 64 {
			// Treat as legitimate inline tag, leave alone.
			return s, false
		}
		// Otherwise, treat as leakage.
	}
	return s[:loc[0]], true
}

// tryUnmarshalStringArray attempts to parse `s` as a JSON array of strings.
// It first runs pattern-1/2/3 cleanup on the value (with paramName="tags"),
// then tries json.Unmarshal. Returns the parsed slice and ok=true on success.
func tryUnmarshalStringArray(s string) ([]any, bool) {
	tryParse := func(input string) ([]any, bool) {
		var arr []any
		if err := json.Unmarshal([]byte(input), &arr); err != nil {
			return nil, false
		}
		// Verify all entries are strings; if any aren't, reject.
		for _, e := range arr {
			if _, ok := e.(string); !ok {
				return nil, false
			}
		}
		return arr, true
	}

	if arr, ok := tryParse(strings.TrimSpace(s)); ok {
		return arr, true
	}

	// Run cleanup and retry.
	clean, _, _ := CleanFreeText(s, "tags")
	if arr, ok := tryParse(strings.TrimSpace(clean)); ok {
		return arr, true
	}
	return nil, false
}

func contains(ss []string, target string) bool {
	for _, s := range ss {
		if s == target {
			return true
		}
	}
	return false
}
