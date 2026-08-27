// Package assets contains the provider-neutral Agent Skill shipped by
// confluence-cli.
package assets

import "embed"

// FS contains every skill file, including dotfiles if any are added later.
//
//go:embed all:skills
var FS embed.FS
