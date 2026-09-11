// Package skill embeds the Claude skill that teaches an agent to use cfl.
package skill

import _ "embed"

// Content is SKILL.md, installed by `cfl install-skill`.
//
//go:embed SKILL.md
var Content string
