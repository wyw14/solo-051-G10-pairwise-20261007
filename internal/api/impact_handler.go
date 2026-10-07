package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/wyw14/cry-161/internal/model"
	"github.com/wyw14/cry-161/internal/recipe"
	"github.com/wyw14/cry-161/internal/service"
)

func revisions(svc *service.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		recipeID, ok := model.ParseID(chi.URLParam(r, "recipeID"))
		if !ok {
			http.Error(w, "invalid recipe", http.StatusBadRequest)
			return
		}
		writeJSON(w, svc.ListRevisions(recipeID))
	}
}

func freezeRevision(svc *service.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		recipeID, ok := model.ParseID(chi.URLParam(r, "recipeID"))
		if !ok {
			http.Error(w, "invalid recipe", http.StatusBadRequest)
			return
		}
		var edited model.Recipe
		if err := json.NewDecoder(r.Body).Decode(&edited); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		edited.ID = recipeID
		frozen, err := svc.ReleaseRecipe(&edited)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		writeJSON(w, frozen)
	}
}

func recipeImpact(svc *service.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		recipeID, ok := model.ParseID(chi.URLParam(r, "recipeID"))
		if !ok {
			http.Error(w, "invalid recipe", http.StatusBadRequest)
			return
		}
		fromNumber := parseRevisionQuery(r, "from")
		toNumber := parseRevisionQuery(r, "to")
		report, err := svc.AnalyzeImpact(recipeID, fromNumber, toNumber)
		if err != nil {
			var unfrozen recipe.ErrUnfrozenRevision
			if errors.As(err, &unfrozen) {
				http.Error(w, err.Error(), http.StatusUnprocessableEntity)
				return
			}
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if r.URL.Query().Get("format") == "text" {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = w.Write([]byte(report.RenderText()))
			return
		}
		writeJSON(w, report)
	}
}

func parseRevisionQuery(r *http.Request, name string) int {
	value := r.URL.Query().Get(name)
	if value == "" {
		return 0
	}
	number, err := strconv.Atoi(value)
	if err != nil || number < 1 {
		return 0
	}
	return number
}
