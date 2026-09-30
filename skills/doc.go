// Package skills registers a "skills" tool on a server.Server: one MCP tool
// that returns a catalog of skill names and descriptions when called with no
// argument and one skill's full body when called with a name. That is
// progressive discovery: an agent pays for a skill's text only when it asks
// for it, and the catalog is small enough to read first.
//
// The pattern was built four times independently before this package existed
// (Hadron's hadron_skills, Tesseract's tesseract_skills, Station's
// atlas_guide, Tether's mux_skill_* trio). This package is a synthesis of the
// shape the first three share, not a port of any of them: the wire shape, the
// "skill_not_found" tool error that points back at the tool, and the two
// stock ways to hold skills. Anything richer -- Tesseract's scope hints and
// related-skill links, Station's structured class:<name> topics, Tether's
// ranked broker and layered project/user discovery -- belongs behind a Source
// the caller writes; this package does not grow to match every shape.
//
// # Sources
//
// A Source lists skills (List) and returns one body by name (Get). Get
// reports an unknown name with an error wrapping ErrNotFound; anything else
// is treated as an internal error. MapSource serves an ordered index and a
// map of bodies held in memory. FSSource serves every top-level *.md file of
// an fs.FS -- typically an embed.FS -- with the file name as the skill name.
// FSSource does not read frontmatter.
//
// # Registering
//
// Register adds the tool through Server.RegisterChecked, as read-only and
// idempotent, and verifies the Source once: every listed name must be
// non-empty, unique and gettable. Call it at startup, then mention the tool
// in the server's instructions and point other tools' errors at it with
// budget.ToolError.WithHelpTool.
//
// A server author's other checks live next to this one: RegisterChecked and
// LintCatalog, whose WithExpectedNames option pins the exact set of tool names
// a server exposes.
package skills
