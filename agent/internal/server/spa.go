package server

import (
	"bytes"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/rithviknishad/care-pi/agent/internal/problem"
)

const notBuilt = `<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title>care-pi console</title></head>
<body>
<h1>The console is not built</h1>
<p>This agent has no console build. The API at /api/v1/ works.</p>
<p>To build the console, run <code>make console-build</code>, then build the agent again.</p>
</body>
</html>
`

// spa serves the console build: the files of dist/, /assets/* with a long cache, and
// index.html for each other path (the routes of the SPA).
func (s *Server) spa(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Cache-Control", "no-store")
		s.problem(w, r, problem.Newf(problem.NotFound, "No operation has the method %s and the path %s.", r.Method, r.URL.Path))
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	index, err := fsRead(s.o.Console, "index.html")
	if err != nil {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(notBuilt))
		return
	}
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name != "" && name != "index.html" {
		if data, err := fsRead(s.o.Console, name); err == nil {
			if strings.HasPrefix(name, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			} else {
				w.Header().Set("Cache-Control", "no-cache")
			}
			http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
			return
		}
		if strings.HasPrefix(name, "assets/") || path.Ext(name) != "" {
			w.Header().Set("Cache-Control", "no-store")
			http.NotFound(w, r)
			return
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(index))
}

func fsRead(fsys fs.FS, name string) ([]byte, error) {
	if fsys == nil {
		return nil, fs.ErrNotExist
	}
	st, err := fs.Stat(fsys, name)
	if err != nil || st.IsDir() {
		return nil, fs.ErrNotExist
	}
	return fs.ReadFile(fsys, name)
}
