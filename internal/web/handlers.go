package web

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/mikeschinkel/endless/internal/web/data"
	"github.com/mikeschinkel/endless/internal/web/pages"
)

func handleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	projects := GetDashboardProjects()
	activities := GetRecentActivity(15)

	// Top 3 for recent projects widget
	recent := projects
	if len(recent) > 3 {
		recent = recent[:3]
	}

	_ = pages.Dashboard(projects, recent, activities).Render(r.Context(), w)
}

func handleProjects(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/status", http.StatusMovedPermanently)
}

func handleStatus(w http.ResponseWriter, r *http.Request) {
	projects := GetDashboardProjects()

	// If a specific project is in the URL, select it; otherwise select the first
	name := r.PathValue("name")
	if name == "" && len(projects) > 0 {
		name = projects[0].Name
	}

	var detail *data.StatusDetail
	if name != "" {
		d, err := GetStatusDetail(name)
		if err == nil {
			detail = d
		}
	}

	_ = pages.StatusPage(projects, name, detail).Render(r.Context(), w)
}

func handleStatusDetail(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		http.NotFound(w, r)
		return
	}

	detail, err := GetStatusDetail(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	_ = pages.StatusDetail(detail).Render(r.Context(), w)
}

func handleProjectDetail(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		http.NotFound(w, r)
		return
	}

	project, err := GetProjectDetail(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	taskItems := GetProjectTasks(project.ID)
	activities := GetProjectActivity(project.ID, 30)
	notes := GetProjectNotes(project.ID)
	deps := GetProjectDependencies(project.ID)

	_ = pages.ProjectDetail(project, taskItems, activities, notes, deps).Render(r.Context(), w)
}

func handleProjectTasks(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		http.NotFound(w, r)
		return
	}

	project, err := GetProjectDetail(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	taskItems := GetProjectTasks(project.ID)
	_ = pages.TaskDetail(project, taskItems).Render(r.Context(), w)
}

func handleTasks(w http.ResponseWriter, r *http.Request) {
	_ = pages.StubPage("Tasks", "/tasks").Render(r.Context(), w)
}

func handleActivity(w http.ResponseWriter, r *http.Request) {
	_ = pages.StubPage("Activity", "/activity").Render(r.Context(), w)
}

func handleNotes(w http.ResponseWriter, r *http.Request) {
	_ = pages.StubPage("Notes", "/notes").Render(r.Context(), w)
}

func handleUpdateTaskTitle(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid ID", http.StatusBadRequest)
		return
	}

	newTitle := strings.TrimSpace(r.FormValue("title"))
	if newTitle == "" {
		http.Error(w, "Title cannot be empty", http.StatusUnprocessableEntity)
		return
	}

	if err := UpdateTaskTitle(id, newTitle); err != nil {
		http.Error(w, "Update failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, newTitle)
}

func handleUpdateTaskStatus(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid ID", http.StatusBadRequest)
		return
	}

	newStatus := strings.TrimSpace(r.FormValue("status"))
	valid := map[string]bool{
		"needs_plan": true, "ready": true, "in_progress": true,
		"verify": true, "confirmed": true, "assumed": true,
		"blocked": true, "revisit": true, "declined": true, "obsolete": true,
	}
	if !valid[newStatus] {
		http.Error(w, "Invalid status", http.StatusUnprocessableEntity)
		return
	}

	if err := UpdateTaskStatus(id, newStatus); err != nil {
		http.Error(w, "Update failed", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}
