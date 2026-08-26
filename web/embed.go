// Package web holds the operator workspace: the sign-in gate and the
// single-page client for the JSON API.
//
// These are real files rather than string constants in Go. The previous
// incarnation of this UI was a 14 KB single-line constant with
// campaign-hierarchy logic written as embedded JavaScript, which could not be
// diffed, linted, or opened in an editor that understood it. Compiling them
// in with embed.FS keeps a deploy to one artefact without paying that price.
package web

import "embed"

//go:embed index.html favicon.svg app styles
var Files embed.FS
