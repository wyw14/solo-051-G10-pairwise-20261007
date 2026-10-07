package api

import (
	"encoding/json"
	"github.com/go-chi/chi/v5"
	"github.com/wyw14/cry-161/internal/impact"
	"github.com/wyw14/cry-161/internal/model"
	"github.com/wyw14/cry-161/internal/service"
	"net/http"
)

type impactRequest struct {
	RecipeID   string `json:"recipe_id"`
	FromNumber int    `json:"from_revision"`
	ToNumber   int    `json:"to_revision"`
}

// freezeRevision freezes the currently published active recipe.
func freezeRevision(svc *service.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		recipeID, ok := model.ParseID(chi.URLParam(r, "recipeID"))
		if !ok {
			http.Error(w, "invalid recipe id", http.StatusBadRequest)
			return
		}
		revision, err := svc.FreezeRecipe(recipeID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		writeJSON(w, revision)
	}
}

// listRevisions returns the real frozen revisions of a recipe.
func listRevisions(svc *service.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		recipeID, ok := model.ParseID(chi.URLParam(r, "recipeID"))
		if !ok {
			http.Error(w, "invalid recipe id", http.StatusBadRequest)
			return
		}
		writeJSON(w, svc.FrozenRevisions(recipeID))
	}
}

// analyzeImpact builds an auditable impact report from two frozen revisions.
// Draft edits can never reach this comparison: resolution only accepts frozen
// revision numbers.
func analyzeImpact(svc *service.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input impactRequest
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		recipeID, ok := model.ParseID(input.RecipeID)
		if !ok {
			http.Error(w, "invalid recipe id", http.StatusBadRequest)
			return
		}
		report, err := svc.AnalyzeImpact(service.ImpactRequest{
			RecipeID:    recipeID,
			FromNumber:  input.FromNumber,
			ToNumber:    input.ToNumber,
			GeneratedBy: r.Header.Get("X-Operator"),
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		writeJSON(w, report)
	}
}

// impactReportText renders one report in the human-auditable text form.
func impactReportText(svc *service.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := model.ParseID(chi.URLParam(r, "reportID"))
		if !ok {
			http.Error(w, "invalid report id", http.StatusBadRequest)
			return
		}
		report, found, err := svc.GetImpactReport(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if !found {
			http.Error(w, "report not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(report.RenderText()))
	}
}

func listImpactReports(svc *service.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var recipeID model.ID
		if raw := r.URL.Query().Get("recipe_id"); raw != "" {
			parsed, ok := model.ParseID(raw)
			if !ok {
				http.Error(w, "invalid recipe id", http.StatusBadRequest)
				return
			}
			recipeID = parsed
		}
		reports, err := svc.ListImpactReports(recipeID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, reports)
	}
}

type bindRequest struct {
	RevisionNumber int `json:"revision_number"`
}

// bindRun attributes an existing run to a specific frozen revision.
func bindRun(svc *service.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		runID, ok := model.ParseID(chi.URLParam(r, "runID"))
		if !ok {
			http.Error(w, "invalid run id", http.StatusBadRequest)
			return
		}
		var input bindRequest
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		binding, err := svc.BindRunToFrozen(runID, input.RevisionNumber, impact.BindingManual)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		writeJSON(w, binding)
	}
}
