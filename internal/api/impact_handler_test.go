package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wyw14/cry-161/internal/model"
	"github.com/wyw14/cry-161/internal/recipe"
	"github.com/wyw14/cry-161/internal/service"
)

type impactEnvelope struct {
	Basis         string `json:"basis"`
	DraftsIgnored int    `json:"drafts_ignored"`
	FromRevision  struct {
		Number int `json:"number"`
	} `json:"from_revision"`
	ToRevision struct {
		Number int `json:"number"`
	} `json:"to_revision"`
	Changes []map[string]any `json:"changes"`
}

func seedRecipeID(t *testing.T, server http.Handler) string {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/api/recipes", nil)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	var listed []model.Recipe
	if json.NewDecoder(response.Body).Decode(&listed) != nil || len(listed) != 1 {
		t.Fatalf("expected one seeded recipe, code=%d body=%s", response.Code, response.Body.String())
	}
	return string(listed[0].ID)
}

func TestImpactHTTPEndpoints(t *testing.T) {
	svc := service.New()
	server := NewRouter(svc)
	recipeID := seedRecipeID(t, server)

	// Save an edited draft: stored but never published/frozen.
	draftBody := `{"name":"AR coating","revision":1,"published":true,"layers":{"1":{"number":1,"material":"SiO2","target_nm":140,"rate_nm_s":5},"2":{"number":2,"material":"TiO2","target_nm":60,"rate_nm_s":3}}}`
	request := httptest.NewRequest(http.MethodPut, "/api/recipes/"+recipeID, bytes.NewBufferString(draftBody))
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("draft save code=%d body=%s", response.Code, response.Body.String())
	}
	var draft model.Recipe
	if json.NewDecoder(response.Body).Decode(&draft) != nil || draft.Published {
		t.Fatalf("draft must be unpublished: %s", response.Body.String())
	}

	// Impact over a draft-only change is refused: still one frozen revision.
	request = httptest.NewRequest(http.MethodGet, "/api/recipes/"+recipeID+"/impact", nil)
	response = httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("impact with one frozen revision code=%d, want 400", response.Code)
	}

	// Promote the edit to frozen revision 2.
	request = httptest.NewRequest(http.MethodPost, "/api/recipes/"+recipeID+"/revisions", bytes.NewBufferString(draftBody))
	response = httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("freeze code=%d body=%s", response.Code, response.Body.String())
	}

	// Both frozen revisions are listed.
	request = httptest.NewRequest(http.MethodGet, "/api/recipes/"+recipeID+"/revisions", nil)
	response = httptest.NewRecorder()
	server.ServeHTTP(response, request)
	var revisions []recipe.Revision
	if json.NewDecoder(response.Body).Decode(&revisions) != nil || len(revisions) != 2 {
		t.Fatalf("expected two frozen revisions, got %s", response.Body.String())
	}

	// Impact now resolves only the frozen versions and notes the ignored draft.
	request = httptest.NewRequest(http.MethodGet, "/api/recipes/"+recipeID+"/impact", nil)
	response = httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("impact code=%d body=%s", response.Code, response.Body.String())
	}
	var report impactEnvelope
	if json.NewDecoder(response.Body).Decode(&report) != nil {
		t.Fatalf("decode report: %s", response.Body.String())
	}
	if report.Basis != recipe.BasisFrozenRevisions {
		t.Fatalf("basis=%q", report.Basis)
	}
	if report.FromRevision.Number != 1 || report.ToRevision.Number != 2 {
		t.Fatalf("window=%d->%d", report.FromRevision.Number, report.ToRevision.Number)
	}
	if report.DraftsIgnored != 1 || len(report.Changes) == 0 {
		t.Fatalf("drafts_ignored=%d changes=%d", report.DraftsIgnored, len(report.Changes))
	}

	// A never-frozen revision number is rejected as unprocessable.
	request = httptest.NewRequest(http.MethodGet, "/api/recipes/"+recipeID+"/impact?from=1&to=99", nil)
	response = httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unfrozen target code=%d, want 422", response.Code)
	}

	// Text rendering is available for audit export.
	request = httptest.NewRequest(http.MethodGet, "/api/recipes/"+recipeID+"/impact?format=text", nil)
	response = httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "AUDIT REPORT") {
		t.Fatalf("text report code=%d body=%s", response.Code, response.Body.String())
	}
}

func TestImpactRejectsBadRecipeID(t *testing.T) {
	server := NewRouter(service.New())
	request := httptest.NewRequest(http.MethodGet, "/api/recipes/not-a-uuid/impact", nil)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("code=%d, want 400", response.Code)
	}
}
