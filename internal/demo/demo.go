// Package demo serves a small page for trying out the API in a browser.
package demo

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed static
var static embed.FS

const contentSecurityPolicy = "default-src 'none'; script-src 'self'; style-src 'self'; " +
	"connect-src 'self'; img-src data:; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

// Handler serves the demo page under /demo/.
func Handler() http.Handler {
	files, err := fs.Sub(static, "static")
	if err != nil {
		panic(err)
	}
	fileServer := http.StripPrefix("/demo/", http.FileServerFS(files))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", contentSecurityPolicy)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		fileServer.ServeHTTP(w, r)
	})
}
