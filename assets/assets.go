// Package assets holds the front-end files every app shares: htmx and hx-live (their shipped
// .min.js, embedded and served by Handler), and daisyUI's standalone plugin files, which the
// apps' Tailwind builds read (@plugin in their main.css).
package assets

import (
	"embed"
	"net/http"

	"github.com/rbxnk/paddock/web"
)

//go:embed *.js
var FS embed.FS

// Prefix is where Handler serves FS.
const Prefix = "/static/kit/"

// Handler serves FS under Prefix; mount it at "GET " + Prefix.
func Handler() http.Handler { return http.StripPrefix(Prefix[:len(Prefix)-1], web.Static(FS)) }

// URL is name's hashed URL under Prefix.
func URL(name string) (string, error) { return web.AssetURL(FS, Prefix, name) }
