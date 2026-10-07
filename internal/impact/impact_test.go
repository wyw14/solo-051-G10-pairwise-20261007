package impact

import (
	"errors"
	"github.com/wyw14/cry-161/internal/model"
	"github.com/wyw14/cry-161/internal/recipe"
	"strings"
	"testing"
	"time"
)

func freeze(t *testing.T, store *recipe.RevisionStore, r *model.Recipe) recipe.Revision {
	t.Helper()
	rev, err := store.Freeze(r)
	if err != nil {
		t.Fatalf("freeze: %v", err)
	}
	return rev
}

func TestResolveFrozenOnlyAcceptsFrozen(t *testing.T) {
	store := recipe.NewRevisionStore()
	r := &model.Recipe{ID: "recipe-1", Name: "AR", Revision: 1, Published: true, Layers: map[int]model.Layer{1: layer(1, "SiO2", 100, 2)}}

	if _, err := ResolveFrozen(store, r.ID, LatestRevision); !errors.Is(err, ErrDraftNotPublished) {
		t.Fatalf("expected ErrDraftNotPublished with no freezes, got %v", err)
	}

	rev1 := freeze(t, store, r)
	r.Revision = 2
	r.Layers[1] = layer(1, "SiO2", 110, 2)
	rev2 := freeze(t, store, r)

	got, err := ResolveFrozen(store, r.ID, LatestRevision)
	if err != nil || got.Number != rev2.Number {
		t.Fatalf("latest = %v, %v", got.Number, err)
	}
	got, err = ResolveFrozen(store, r.ID, rev1.Number)
	if err != nil || got.Number != 1 {
		t.Fatalf("explicit = %v, %v", got.Number, err)
	}
	if _, err := ResolveFrozen(store, r.ID, 99); !errors.Is(err, ErrRevisionNotFrozen) {
		t.Fatalf("expected ErrRevisionNotFrozen, got %v", err)
	}
}

func TestResolvePairRejectsNonAscending(t *testing.T) {
	store := recipe.NewRevisionStore()
	r := &model.Recipe{ID: "recipe-1", Revision: 1, Published: true, Layers: map[int]model.Layer{1: layer(1, "SiO2", 100, 2)}}
	freeze(t, store, r)
	r.Revision = 2
	freeze(t, store, r)
	if _, _, err := ResolvePair(store, r.ID, 2, 1); err == nil {
		t.Fatal("expected ascending-pair error")
	}
}

// TestDraftCannotBeCompared is the core audit guarantee: a draft-only edit has
// no frozen revision number, so resolution fails and no report can cite it.
func TestDraftCannotBeCompared(t *testing.T) {
	store := recipe.NewRevisionStore()
	r := &model.Recipe{ID: "recipe-1", Revision: 1, Published: true, Layers: map[int]model.Layer{1: layer(1, "SiO2", 100, 2)}}
	freeze(t, store, r)

	// Simulate a draft change WITHOUT freezing: bump revision and edit layer.
	r.Published = false
	r.Revision = 7
	r.Layers[1] = layer(1, "HACK", 999, 9)

	// Asking for "the newest revision" still returns frozen rev 1, never rev 7.
	got := store.List(r.ID)
	if len(got) != 1 || got[0].Number != 1 {
		t.Fatalf("draft must not enter frozen store: %+v", got)
	}
	resolved, err := ResolveFrozen(store, r.ID, 7)
	if !errors.Is(err, ErrRevisionNotFrozen) {
		t.Fatalf("draft revision 7 must not resolve, got %+v err=%v", resolved, err)
	}
}

func TestBinderForbidsRebinding(t *testing.T) {
	binder := NewBinder()
	if _, err := binder.Bind("run-1", "recipe-1", 1, BindingAtStart); err != nil {
		t.Fatal(err)
	}
	if _, err := binder.Bind("run-1", "recipe-1", 2, BindingManual); err == nil {
		t.Fatal("rebinding to a different revision must fail")
	}
	// Same target is idempotent.
	if _, err := binder.Bind("run-1", "recipe-1", 1, BindingManual); err != nil {
		t.Fatalf("idempotent rebind failed: %v", err)
	}
}

func TestAssessRunAndSafetyImpact(t *testing.T) {
	from := RevisionView{RecipeID: "recipe-1", Number: 1, Layers: []model.Layer{layer(1, "SiO2", 100, 0.5), layer(2, "TiO2", 60, 3)}}
	to := RevisionView{RecipeID: "recipe-1", Number: 2, Layers: []model.Layer{layer(1, "SiO2", 140, 2), layer(2, "TiO2", 60, 3)}}
	diff := ComputeDiff(from, to)

	inFlight := &model.Run{ID: "run-active", Batch: "B1", RecipeID: "recipe-1", State: model.Coating, Layer: 1, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	done := &model.Run{ID: "run-done", Batch: "B2", RecipeID: "recipe-1", State: model.Released}
	runs := []*model.Run{inFlight, done}
	bindings := map[model.ID]RunBinding{
		"run-active": {RunID: "run-active", RecipeID: "recipe-1", RevisionNumber: 1, Source: BindingAtStart},
		"run-done":   {RunID: "run-done", RecipeID: "recipe-1", RevisionNumber: 1, Source: BindingAtStart},
	}
	assessment := Assess(diff, runs, bindings)
	if len(assessment.AffectedRuns) != 2 {
		t.Fatalf("expected 2 affected runs, got %d", len(assessment.AffectedRuns))
	}
	active := assessment.AffectedRuns[0]
	if active.Exposure != ExposureInFlight || active.ChangedLayers[0] != 1 {
		t.Fatalf("unexpected active impact %+v", active)
	}
	// Only layer 1 changed; an in-flight run at layer 2 would have passed it.
	lateRun := &model.Run{ID: "run-late", Batch: "B3", RecipeID: "recipe-1", State: model.Coating, Layer: 2}
	lateAssessment := Assess(diff, []*model.Run{lateRun}, map[model.ID]RunBinding{
		"run-late": {RunID: "run-late", RecipeID: "recipe-1", RevisionNumber: 1},
	})
	if len(lateAssessment.AffectedRuns) != 1 || len(lateAssessment.AffectedRuns[0].ChangedLayers) != 0 {
		t.Fatalf("run past the changed layer should have no executable delta: %+v", lateAssessment.AffectedRuns)
	}

	// Target rose 100 -> 140: overcoat cutoff must be re-confirmed (critical).
	var overcoat *InterlockImpact
	for i := range assessment.Interlocks {
		if assessment.Interlocks[i].Interlock == InterlockOvercoat {
			overcoat = &assessment.Interlocks[i]
		}
	}
	if overcoat == nil || overcoat.Severity != "critical" {
		t.Fatalf("expected critical overcoat impact, got %+v", assessment.Interlocks)
	}
	// Rate crossed the 1 nm/s rotation threshold on layer 1.
	var rotation *InterlockImpact
	for i := range assessment.Interlocks {
		if assessment.Interlocks[i].Interlock == InterlockRotation {
			rotation = &assessment.Interlocks[i]
		}
	}
	if rotation == nil {
		t.Fatalf("expected rotation profile impact, got %+v", assessment.Interlocks)
	}
	// Quality gates cite the real release checks.
	gates := map[GateName]bool{}
	for _, gate := range assessment.QualityGates {
		gates[gate.Gate] = true
	}
	for _, want := range []GateName{GateLayerEvidence, GateOpticalRelease, GateRunState} {
		if !gates[want] {
			t.Fatalf("missing quality gate %s in %+v", want, assessment.QualityGates)
		}
	}
}

func TestBuildReportValidatesAndExcludesDrafts(t *testing.T) {
	now := time.Now().UTC()
	fromLayers := []model.Layer{layer(1, "SiO2", 100, 2)}
	toLayers := []model.Layer{layer(1, "SiO2", 120, 2)}
	report := BuildReport(BuildInputs{
		GeneratedBy: "qa",
		From:        frozenRevision{id: "rev-1", recipeID: "recipe-1", number: 1, frozenAt: now, layers: fromLayers},
		To:          frozenRevision{id: "rev-2", recipeID: "recipe-1", number: 2, frozenAt: now, layers: toLayers},
		DraftIDs:    []model.ID{"recipe-9"},
		Runs: []*model.Run{
			{ID: "run-unbound", Batch: "B", RecipeID: "recipe-1", State: model.Loading},
		},
		Bindings: map[model.ID]RunBinding{},
	})
	if err := report.Validate(); err != nil {
		t.Fatalf("valid report rejected: %v", err)
	}
	if report.From.Source != "frozen_revision_store" || report.From.Fingerprint == "" {
		t.Fatalf("provenance not frozen: %+v", report.From)
	}
	if report.From.Fingerprint == report.To.Fingerprint {
		t.Fatal("fingerprints must differ when layers differ")
	}
	if len(report.Unbound) != 1 || report.Unbound[0].RunID != "run-unbound" {
		t.Fatalf("expected one unbound run, got %+v", report.Unbound)
	}
	if len(report.Drafts.DraftRecipeIDs) != 1 || !strings.Contains(report.Drafts.Note, "not represented as published changes") {
		t.Fatalf("draft exclusion not recorded: %+v", report.Drafts)
	}
	if !strings.Contains(report.RenderText(), "DRAFT EXCLUSION") || !strings.Contains(report.RenderText(), "UNBOUND RUNS") {
		t.Fatal("text report missing audit sections:\n" + report.RenderText())
	}
}

func TestReportValidateRejectsNonFrozenSource(t *testing.T) {
	now := time.Now().UTC()
	report := BuildReport(BuildInputs{
		From: frozenRevision{id: "rev-1", recipeID: "recipe-1", number: 1, frozenAt: now, layers: []model.Layer{layer(1, "SiO2", 100, 2)}},
		To:   frozenRevision{id: "rev-2", recipeID: "recipe-1", number: 2, frozenAt: now, layers: []model.Layer{layer(1, "SiO2", 120, 2)}},
	})
	report.From.Source = "draft"
	if err := report.Validate(); err == nil || !strings.Contains(err.Error(), "frozen revisions") {
		t.Fatalf("expected frozen-only validation error, got %v", err)
	}
}
