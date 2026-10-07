package impact

import (
	"github.com/wyw14/cry-161/internal/model"
	"github.com/wyw14/cry-161/internal/recipe"
	"path/filepath"
	"testing"
	"time"
)

func validReport(t *testing.T) Report {
	t.Helper()
	now := time.Now().UTC()
	report := BuildReport(BuildInputs{
		GeneratedBy: "qa",
		From:        frozenRevision{id: "rev-1", recipeID: "recipe-1", number: 1, frozenAt: now, layers: []model.Layer{layer(1, "SiO2", 100, 2)}},
		To:          frozenRevision{id: "rev-2", recipeID: "recipe-1", number: 2, frozenAt: now, layers: []model.Layer{layer(1, "SiO2", 120, 2)}},
	})
	if err := report.Validate(); err != nil {
		t.Fatalf("valid report: %v", err)
	}
	return report
}

func TestReportStoreRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "impact")
	store := NewReportStore(dir)
	report := validReport(t)
	if err := store.Save(report); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded, ok, err := store.Get(report.ID)
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if loaded.From.Fingerprint != report.From.Fingerprint || loaded.To.Number != 2 {
		t.Fatalf("round trip mismatch: %+v", loaded)
	}
	list, err := store.List("recipe-1")
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %d err=%v", len(list), err)
	}
	if other, err := store.List(model.ID("other-recipe")); err != nil || len(other) != 0 {
		t.Fatalf("recipe filter failed: %d %v", len(other), err)
	}
}

func TestReportStoreRejectsInvalidReport(t *testing.T) {
	store := NewReportStore(t.TempDir())
	report := validReport(t)
	report.From.Source = "draft"
	if err := store.Save(report); err == nil {
		t.Fatal("non-frozen report must not be archived")
	}
}

func TestAnalyzeEndToEnd(t *testing.T) {
	// Build a recipe, freeze rev 1, publish a change, freeze rev 2.
	r := &model.Recipe{ID: "recipe-1", Name: "AR", Revision: 1, Published: true,
		Layers: map[int]model.Layer{1: layer(1, "SiO2", 100, 2), 2: layer(2, "TiO2", 60, 3)}}
	revisions := recipe.NewRevisionStore()
	freeze(t, revisions, r)
	r.Revision = 2
	r.Layers[1] = layer(1, "SiO2", 130, 2)
	freeze(t, revisions, r)

	binder := NewBinder()
	boundRun := &model.Run{ID: "run-bound", Batch: "B1", RecipeID: "recipe-1", State: model.Coating, Layer: 1}
	runs := []*model.Run{
		boundRun,
		{ID: "run-unbound", Batch: "B2", RecipeID: "recipe-1", State: model.Loading},
	}
	if _, err := binder.Bind("run-bound", "recipe-1", 1, BindingAtStart); err != nil {
		t.Fatal(err)
	}

	report, err := Analyze(revisions, binder, runs, []model.ID{"recipe-1"}, Request{
		RecipeID: "recipe-1", FromNumber: 1, ToNumber: 2, GeneratedBy: "qa",
	})
	if err != nil {
		t.Fatalf("analyze latest pair: %v", err)
	}
	if report.From.Number != 1 || report.To.Number != 2 {
		t.Fatalf("unexpected pair %d->%d", report.From.Number, report.To.Number)
	}
	if !containsDimension(report.Diff, DimensionTarget) {
		t.Fatalf("missing target dimension: %v", report.Diff.Dimensions)
	}
	if len(report.Impact.AffectedRuns) != 1 || report.Impact.AffectedRuns[0].RunID != "run-bound" {
		t.Fatalf("only the bound run should be affected: %+v", report.Impact.AffectedRuns)
	}
	if len(report.Unbound) != 1 || report.Unbound[0].RunID != "run-unbound" {
		t.Fatalf("unbound run must be surfaced: %+v", report.Unbound)
	}
	// Archiving and reload proves the report is persistently auditable.
	dir := filepath.Join(t.TempDir(), "impact")
	archive := NewReportStore(dir)
	if err := archive.Save(report); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := archive.Get(report.ID); err != nil || !ok {
		t.Fatalf("reload ok=%v err=%v", ok, err)
	}
}
