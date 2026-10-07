package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/wyw14/cry-161/internal/model"
	"github.com/wyw14/cry-161/internal/service"
)

func recipes(svc *service.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { writeJSON(w, svc.RecipesList()) }
}

// recipeEdit stores an edited recipe as an unpublished draft. It deliberately
// does NOT publish or freeze a revision, so a draft edit can never be cited as
// a released change by the impact analysis. Promotion happens only through
// POST /api/recipes/{id}/revisions.
func recipeEdit(svc *service.Service) http.HandlerFunc {
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
		if len(edited.Layers) == 0 {
			http.Error(w, "recipe has no layers", http.StatusBadRequest)
			return
		}
		draft, err := svc.SaveDraft(&edited)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		writeJSON(w, draft)
	}
}
