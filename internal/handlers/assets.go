package handlers

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"html/template"
	"io/fs"
	"net/http"
	"path"
	"strings"

	financialanalyzer "github.com/VxVxN/financialanalyzer"
)

// staticFS is the embedded static/ directory, rooted so that "app.css" names
// static/app.css.
var staticFS = mustSub(financialanalyzer.StaticFS, "static")

// assetVersions maps each static file to a short hash of its content. Pages
// link assets as /static/<name>?v=<hash>, so a new build busts the browser
// cache while an unchanged asset stays cached for good.
var assetVersions = hashAssets(staticFS)

func mustSub(fsys fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic(err) // the directory is embedded at build time
	}
	return sub
}

func hashAssets(fsys fs.FS) map[string]string {
	versions := map[string]string{}
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		versions[p] = hex.EncodeToString(sum[:])[:12]
		return nil
	})
	if err != nil {
		panic(err)
	}
	return versions
}

// assetURL is the cache-busting URL of a static file ("app.css").
func assetURL(name string) string {
	return "/static/" + name + "?v=" + assetVersions[name]
}

// StaticHandler serves the embedded static files under /static/. A request
// carrying the file's current version is cached as immutable; anything else
// (fonts linked from the stylesheet, an old version) is cached briefly.
// Directory listings are not served.
func StaticHandler() http.Handler {
	files := http.FileServerFS(staticFS)
	return http.StripPrefix("/static/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		version, ok := assetVersions[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("v") == version {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "public, max-age=86400")
		}
		if path.Ext(name) == ".woff2" {
			// Not in Go's built-in MIME table.
			w.Header().Set("Content-Type", "font/woff2")
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		files.ServeHTTP(w, r)
	}))
}

// templateFuncs are available to every page template.
var templateFuncs = template.FuncMap{"asset": assetURL}

// parsePage parses an embedded page template together with the shared layout
// (templates/layout.html: head, topbar, footer). A parse failure is a
// programmer error (the templates ship inside the binary), so it panics.
func parsePage(name string) *template.Template {
	return template.Must(template.New(name).Funcs(templateFuncs).
		ParseFS(financialanalyzer.TemplatesFS, "templates/layout.html", "templates/"+name))
}

// layoutTemplate renders the shared layout blocks for pages written directly
// to the response (the company dashboard).
var layoutTemplate = template.Must(template.New("layout").Funcs(templateFuncs).
	ParseFS(financialanalyzer.TemplatesFS, "templates/layout.html"))

// pageMeta is the data of the shared layout blocks.
type pageMeta struct {
	Title  string // <title>, before the app name
	Active string // highlighted nav item: screener | compare | updates | ""
}

// writeLayout renders one shared layout block ("head", "topbar", "footer").
func writeLayout(w http.ResponseWriter, block string, meta pageMeta) error {
	var buf bytes.Buffer
	if err := layoutTemplate.ExecuteTemplate(&buf, block, meta); err != nil {
		return err
	}
	_, err := buf.WriteTo(w)
	return err
}
