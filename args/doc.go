// Package args reads typed values out of a tool call's decoded argument map
// (the map[string]any a server.ToolHandler receives).
//
// Apps kept private copies of these helpers whose coercions quietly differed:
// one accepted the string "50" for a number, another trimmed, another
// clamped. Here every coercion has its own name, so a call site says which
// behavior it depends on:
//
//   - String, Bool, Float, Int, Strings: the value must already have the
//     right JSON type (numbers may arrive as float64, int, int64 or
//     json.Number); anything else yields the default.
//   - Trimmed, NonBlank, NonBlankStrings: string reads that trim or treat
//     blank as absent.
//   - IntClamped, PositiveInt: integer reads with a range or a > 0 rule.
//   - Whole: an integer read that refuses 2.5 instead of truncating it.
//   - LenientInt, LenientFloat, LenientBool: also accept numeric and boolean
//     strings, for clients that send "50" or "true".
//   - Require: reports absent or blank required arguments as a
//     *budget.ToolError.
//
// A key that is absent, JSON null, or of the wrong type always yields the
// stated default (or the zero value where no default is taken); no accessor
// panics. The package depends only on the standard library and budget.
package args
