package service

import (
	"context"
	"github.com/wyw14/cry-161/internal/impact"
	"github.com/wyw14/cry-161/internal/model"
	"strings"
	"testing"
)

func newTestService(t *testing.T) *Service {
	t.Helper()
	t.Setenv("OPTICCOAT_DATA", t.TempDir())
	return New()
}

// TestImpactAnalysisEndToEnd drives the full service path: freeze rev 1, start
// a run (auto-bound), edit only a draft, freeze rev 2, then analyze. The report
// must cite both frozen revisions, include the bound run as affected, list the
// draft id in the exclusion section, and be archived.
func TestImpactAnalysisEndToEnd(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	recipeID := svc.Recipes.List()[0].ID

	rev1, err := svc.FreezeRecipe(recipeID)
	if err != nil {
		t.Fatalf("freeze rev1: %v", err)
	}

	runValue, err := svc.Start(ctx, "BATCH-1", recipeID)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	binding, ok := svc.RunBindings.Binding(runValue.ID)
	if !ok || binding.RevisionNumber != rev1.Number {
		t.Fatalf("run must auto-bind to frozen rev %d, got %+v ok=%v", rev1.Number, binding, ok)
	}

	// Mutate the DRAFT only. This must not appear as a published change.
	draft := svc.Drafts.Save(svc.Recipes.List()[0])
	if !svc.Drafts.UpdateLayer(draft.ID, model.Layer{Number: 1, Material: "HACK", TargetNanometers: 999, RateNanometers: 9}) {
		t.Fatal("draft update failed")
	}

	// Publish a real change and freeze rev 2.
	active, _ := svc.Recipes.Active(recipeID)
	active.Revision = 2
	active.Layers[1] = model.Layer{Number: 1, Material: "SiO2", TargetNanometers: 130, RateNanometers: 2}
	svc.Recipes.Publish(active)
	rev2, err := svc.FreezeRecipe(recipeID)
	if err != nil {
		t.Fatalf("freeze rev2: %v", err)
	}

	// Analyzing an unfrozen number fails; a draft revision (3) is not frozen.
	if _, err := svc.AnalyzeImpact(ImpactRequest{RecipeID: recipeID, FromNumber: 1, ToNumber: 3}); err == nil {
		t.Fatal("analysis against an unfrozen revision must fail")
	}

	report, err := svc.AnalyzeImpact(ImpactRequest{RecipeID: recipeID, FromNumber: rev1.Number, ToNumber: rev2.Number, GeneratedBy: "qa"})
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	if report.From.Number != 1 || report.To.Number != 2 || report.From.Source != "frozen_revision_store" {
		t.Fatalf("provenance wrong: %+v -> %+v", report.From, report.To)
	}
	if len(report.Impact.AffectedRuns) != 1 || report.Impact.AffectedRuns[0].RunID != runValue.ID {
		t.Fatalf("expected the bound run as affected, got %+v", report.Impact.AffectedRuns)
	}
	if len(report.Drafts.DraftRecipeIDs) == 0 || !strings.Contains(report.Drafts.Note, "not represented as published changes") {
		t.Fatalf("draft exclusion missing: %+v", report.Drafts)
	}
	if !strings.Contains(report.RenderText(), report.From.Fingerprint) {
		t.Fatal("text report must carry frozen fingerprints")
	}

	// Archived and retrievable.
	loaded, found, err := svc.GetImpactReport(report.ID)
	if err != nil || !found || loaded.ID != report.ID {
		t.Fatalf("archive lookup found=%v err=%v", found, err)
	}
	list, err := svc.ListImpactReports(recipeID)
	if err != nil || len(list) != 1 {
		t.Fatalf("archive list len=%d err=%v", len(list), err)
	}
}

func TestFreezeRejectsDraftAndDuplicates(t *testing.T) {
	svc := newTestService(t)
	recipeID := svc.Recipes.List()[0].ID
	if _, err := svc.FreezeRecipe(recipeID); err != nil {
		t.Fatal(err)
	}
	// Same published Revision number cannot be frozen twice.
	if _, err := svc.FreezeRecipe(recipeID); err == nil {
		t.Fatal("duplicate freeze must be rejected")
	}
	// Unknown recipe cannot be frozen.
	if _, err := svc.FreezeRecipe(model.NewID()); err == nil {
		t.Fatal("freeze of unknown recipe must fail")
	}
}

func TestBindRunToFrozenRejectsUnfrozen(t *testing.T) {
	svc := newTestService(t)
	recipeID := svc.Recipes.List()[0].ID
	runValue, err := svc.Start(context.Background(), "BATCH-9", recipeID)
	if err != nil {
		t.Fatal(err)
	}
	// No freezes yet: manual bind to rev 5 must fail.
	if _, err := svc.BindRunToFrozen(runValue.ID, 5, impact.BindingManual); err == nil {
		t.Fatal("binding to an unfrozen revision must fail")
	}
	// The auto-bind at start also left it unbound (no freeze existed).
	if _, ok := svc.RunBindings.Binding(runValue.ID); ok {
		t.Fatal("run should stay honestly unbound without a frozen baseline")
	}
}
