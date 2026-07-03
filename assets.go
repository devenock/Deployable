// Package assets embeds templates/ and static/ for use by cmd/server.
//
// go:embed can only reference files within the embedding source file's own
// directory tree, so this must live at the module root (a sibling of
// templates/ and static/) rather than inside cmd/server/, which is one
// directory removed from them.
package assets

import "embed"

//go:embed templates static
var Files embed.FS

// InstallScript is scripts/install.sh, served at GET /install.sh so users can
// `curl -sSL <app-url>/install.sh | bash`. Embedded separately from Files
// (rather than adding scripts/ wholesale) so backup.sh — an operator-only
// script — is never reachable over HTTP.
//
//go:embed scripts/install.sh
var InstallScript embed.FS
