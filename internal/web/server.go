package web

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
)

// Serve starts the web dashboard on the given port.
func Serve(port int) error {
	mux := http.NewServeMux()

	// Static assets (CSS)
	assetsDir := findAssetsDir()
	mux.Handle("/assets/", http.StripPrefix("/assets/", http.FileServer(http.Dir(assetsDir))))

	// templUI component JS files (served from the templui symlink in css dir)
	templuiJSDir := filepath.Join(assetsDir, "css", "templui", "components")
	mux.HandleFunc("/templui/js/", func(w http.ResponseWriter, r *http.Request) {
		// /templui/js/progress.min.js → components/progress/progress.min.js
		name := filepath.Base(r.URL.Path)                         // progress.min.js
		component := name[:len(name)-len(filepath.Ext(name))]     // progress.min
		if idx := len(component) - 4; idx > 0 && component[idx:] == ".min" {
			component = component[:idx] // progress
		}
		jsPath := filepath.Join(templuiJSDir, component, name)
		http.ServeFile(w, r, jsPath)
	})

	// Pages
	mux.HandleFunc("/", handleDashboard)
	mux.HandleFunc("/projects", handleProjects) // redirects to /status
	mux.HandleFunc("/status", handleStatus)
	mux.HandleFunc("/status/{name}", handleStatus)
	mux.HandleFunc("/status/{name}/detail", handleStatusDetail)
	mux.HandleFunc("/project/{name}", handleProjectDetail)
	mux.HandleFunc("/project/{name}/tasks", handleProjectTasks)
	mux.HandleFunc("/tasks", handleTasks)
	mux.HandleFunc("/activity", handleActivity)
	mux.HandleFunc("/notes", handleNotes)
	mux.HandleFunc("PUT /tasks/{id}/title", handleUpdateTaskTitle)
	mux.HandleFunc("PUT /tasks/{id}/status", handleUpdateTaskStatus)

	addr := fmt.Sprintf(":%d", port)
	fmt.Printf("Endless dashboard: http://localhost%s (pid %d)\n", addr, os.Getpid())
	return http.ListenAndServe(addr, mux)
}

// findAssetsDir locates the assets directory relative to this source file.
func findAssetsDir() string {
	// Try relative to the source file location
	_, thisFile, _, ok := runtime.Caller(0)
	if ok {
		dir := filepath.Join(filepath.Dir(thisFile), "assets")
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir
		}
	}
	// Fallback: relative to working directory
	return "internal/web/assets"
}
