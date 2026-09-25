// Package web embeds the built single-page UI. The real build output is
// copied into web/dist by the Docker image build; the repository only ships
// a placeholder index.html so the embed directive always compiles.
package web

import "embed"

//go:embed all:dist
var Dist embed.FS
