package api

import (
	"bytes"
	"encoding/json"
	"github.com/wyw14/cry-161/internal/model"
	"github.com/wyw14/cry-161/internal/service"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestRouter(t *testing.T) (http.Handler, *service.Service) {
	t.Helper()
	t.Setenv("OPTICCOAT_DATA", t.TempDir())
	svc := service.New()
	return NewRouter(svc), svc
}

func do(t *testing.T, router http.Handler, method, path string, body any, operator string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		reader = bytes.NewReader(data)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	if operator != "" {
		req.Header.Set("X-Operator", operator)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func firstRecipeID(t *testing.T, svc *service.Service) model.ID {
	t.Helper()
	list := svc.Recipes.List()
	if len(list) != 1 {
		t.Fatalf("expected 1 seed recipe, got %d", len(list))
	}
	return list[0].ID
}

func TestImpactAPIFrozenPairFlow(t *testing.T) {
	router, svc := newTestRouter(t)
	recipeID := firstRecipeID(t, svc)

	// Freeze rev 1.
	rec := do(t, router, http.MethodPost, "/api/recipes/"+string(recipeID)+"/freeze", nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("freeze rev1 status=%d body=%s", rec.Code, rec.Body.String())
	}

	// Start a run so it auto-binds to rev 1.
	rec = do(t, router, http.MethodPost, "/api/runs", map[string]string{"batch": "B-1", "recipe_id": string(recipeID)}, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("start status=%d body=%s", rec.Code, rec.Body.String())
	}
	var runValue model.Run
	if json.Unmarshal(rec.Body.Bytes(), &runValue) != nil {
		t.Fatal("decoding run")
	}

	// Publish rev 2 via service, then freeze through the API.
	active, _ := svc.Recipes.Active(recipeID)
	active.Revision = 2
	active.Layers[1] = model.Layer{Number: 1, Material: "SiO2", TargetNanometers: 140, RateNanometers: 2}
	svc.Recipes.Publish(active)
	rec = do(t, router, http.MethodPost, "/api/recipes/"+string(recipeID)+"/freeze", nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("freeze rev2 status=%d body=%s", rec.Code, rec.Body.String())
	}

	// Analyze the two frozen revisions.
	rec = do(t, router, http.MethodPost, "/api/impact/analyze", map[string]any{
		"recipe_id": string(recipeID), "from_revision": 1, "to_revision": 2,
	}, "qa")
	if rec.Code != http.StatusOK {
		t.Fatalf("analyze status=%d body=%s", rec.Code, rec.Body.String())
	}
	var report struct {
		ID   string `json:"id"`
		From struct {
			Source      string `json:"source"`
			Number      int    `json:"number"`
			Fingerprint string `json:"fingerprint_sha256"`
		} `json:"from"`
		To struct {
			Number int `json:"number"`
		} `json:"to"`
		Impact struct {
			AffectedRuns []struct {
				RunID string `json:"run_id"`
			} `json:"affected_runs"`
		} `json:"impact"`
		Drafts struct {
			Note string `json:"note"`
		} `json:"draft_exclusion"`
	}
	if json.Unmarshal(rec.Body.Bytes(), &report) != nil {
		t.Fatal("decoding report")
	}
	if report.From.Source != "frozen_revision_store" || report.From.Number != 1 || report.To.Number != 2 {
		t.Fatalf("bad provenance: %+v", report)
	}
	if len(report.Impact.AffectedRuns) != 1 || report.Impact.AffectedRuns[0].RunID != string(runValue.ID) {
		t.Fatalf("affected runs wrong: %+v", report.Impact.AffectedRuns)
	}
	if !strings.Contains(report.Drafts.Note, "not represented as published changes") {
		t.Fatalf("draft exclusion note missing: %q", report.Drafts.Note)
	}
	if report.From.Fingerprint == "" {
		t.Fatal("fingerprint missing")
	}

	// Text report endpoint.
	rec = do(t, router, http.MethodGet, "/api/impact/reports/"+report.ID, nil, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "RECIPE IMPACT ANALYSIS REPORT") {
		t.Fatalf("text report status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "overcoat_shutter_cutoff") {
		t.Fatalf("text report should name the overcoat interlock (target rose):\n%s", rec.Body.String())
	}

	// Reports list.
	rec = do(t, router, http.MethodGet, "/api/impact/reports", nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list reports status=%d", rec.Code)
	}
	var list []map[string]any
	json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list) != 1 {
		t.Fatalf("expected 1 archived report, got %d", len(list))
	}
}

func TestImpactAPIRejectsUnfrozenRevision(t *testing.T) {
	router, svc := newTestRouter(t)
	recipeID := firstRecipeID(t, svc)
	// Only freeze rev 1.
	if rec := do(t, router, http.MethodPost, "/api/recipes/"+string(recipeID)+"/freeze", nil, ""); rec.Code != http.StatusOK {
		t.Fatalf("freeze status=%d", rec.Code)
	}
	// Compare 1 -> 2 where rev 2 only exists as a draft bump: must be rejected.
	active, _ := svc.Recipes.Active(recipeID)
	active.Revision = 2
	svc.Drafts.Save(active) // draft only, never frozen
	rec := do(t, router, http.MethodPost, "/api/impact/analyze", map[string]any{
		"recipe_id": string(recipeID), "from_revision": 1, "to_revision": 2,
	}, "")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for unfrozen to-revision, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "not frozen") {
		t.Fatalf("error should explain the revision is not frozen: %s", rec.Body.String())
	}
}

func TestImpactRevisionListing(t *testing.T) {
	router, svc := newTestRouter(t)
	recipeID := firstRecipeID(t, svc)
	if rec := do(t, router, http.MethodPost, "/api/recipes/"+string(recipeID)+"/freeze", nil, ""); rec.Code != 200 {
		t.Fatalf("freeze: %d", rec.Code)
	}
	rec := do(t, router, http.MethodGet, "/api/recipes/"+string(recipeID)+"/revisions", nil, "")
	if rec.Code != 200 {
		t.Fatalf("list revisions: %d", rec.Code)
	}
	var revisions []map[string]any
	json.Unmarshal(rec.Body.Bytes(), &revisions)
	if len(revisions) != 1 {
		t.Fatalf("expected 1 frozen revision, got %d", len(revisions))
	}
}
