package web

import (
	"net/http"

	"github.com/mikeschinkel/endless/internal/web/pages"
)

func handleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	projects := GetDashboardProjects()
	activities := GetRecentActivity(15)
	currentWork := GetCurrentWork()

	_ = pages.Dashboard(projects, activities, currentWork).Render(r.Context(), w)
}

func handleProjects(w http.ResponseWriter, r *http.Request) {
	projects := GetDashboardProjects()
	_ = pages.Projects(projects).Render(r.Context(), w)
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

	planItems := GetProjectPlanItems(project.ID)
	activities := GetProjectActivity(project.ID, 30)
	notes := GetProjectNotes(project.ID)
	deps := GetProjectDependencies(project.ID)

	_ = pages.ProjectDetail(project, planItems, activities, notes, deps).Render(r.Context(), w)
}

func handleProjectPlan(w http.ResponseWriter, r *http.Request) {
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

	planItems := GetProjectPlanItems(project.ID)
	_ = pages.PlanDetail(project, planItems).Render(r.Context(), w)
}

func handlePlans(w http.ResponseWriter, r *http.Request) {
	_ = pages.StubPage("Plans", "/plans").Render(r.Context(), w)
}

func handleActivity(w http.ResponseWriter, r *http.Request) {
	_ = pages.StubPage("Activity", "/activity").Render(r.Context(), w)
}

func handleNotes(w http.ResponseWriter, r *http.Request) {
	_ = pages.StubPage("Notes", "/notes").Render(r.Context(), w)
}
