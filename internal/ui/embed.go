// Package ui embeds the admin web interface static files into the binary.
package ui

import "embed"

//go:embed web
var FS embed.FS
