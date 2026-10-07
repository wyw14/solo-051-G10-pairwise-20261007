package recipe

import (
	"errors"
	"strings"
	"testing"

	"github.com/wyw14/cry-161/internal/model"
)

func layer(number int, material string, target, rate float64) model.Layer {
	return model.Layer{Number: number, Material: material, TargetNanometers: target, RateNanometers: rate}
}

func recipeWith(recipeID model.ID, revision int, layers ...model.Layer) *model.Recipe {
	mapped := make(map[int]model.Layer, len(layers))
	for _, item := range layers {
		mapped[item.Number] = item
	}
	return &model.Recipe{ID: recipeID, Name: "test", Revision: revision, Published: true, Layers: mapped}
}

func freeze(t *testing.T, store *RevisionStore, value *model.Recipe) Revision {
	t.Helper()
	frozen, err := store.Freeze(value)
	if err != nil {
		t.Fatalf("freeze revision %d: %v", value.Revision, err)
	}
	return frozen
}

func kinds(changes []Change) map[string]int {
	counts := map[string]int{}
	for _, change := range changes {
		counts[change.Kind]++
	}
	return counts
}

func TestAnalyzeRejectsUnfrozenRevision(t *testing.T) {
	store := NewRevisionStore()
	recipeID := model.NewID()
	freeze(t, store, recipeWith(recipeID, 1, layer(1, "SiO2", 100, 2)))

	// Revision 2 exists only as an imagined/draft number: it was never frozen.
	_, err := Analyze(store, recipeID, 1, 2, nil)
	var unfrozen ErrUnfrozenRevision
	if !errors.As(err, &unfrozen) {
		t.Fatalf("expected ErrUnfrozenRevision, got %v", err)
	}
	if !strings.Contains(err.Error(), "drafts cannot be compared") {
		t.Fatalf("error should explain drafts are excluded: %v", err)
	}
}

func TestAnalyzeRejectsGapBetweenFrozenVersions(t *testing.T) {
	store := NewRevisionStore()
	recipeID := model.NewID()
	freeze(t, store, recipeWith(recipeID, 1, layer(1, "SiO2", 100, 2)))
	freeze(t, store, recipeWith(recipeID, 3, layer(1, "SiO2", 110, 2)))

	// Revision 2 was never released; comparing through it must fail closed.
	if _, err := Analyze(store, recipeID, 1, 2, nil); !errors.As(err, new(ErrUnfrozenRevision)) {
		t.Fatalf("expected unfrozen error for missing rev 2, got %v", err)
	}
}

func TestAnalyzeRejectsSameRevision(t *testing.T) {
	store := NewRevisionStore()
	recipeID := model.NewID()
	freeze(t, store, recipeWith(recipeID, 1, layer(1, "SiO2", 100, 2)))
	if _, err := Analyze(store, recipeID, 1, 1, nil); err == nil {
		t.Fatal("expected error comparing a revision to itself")
	}
}

func TestDiffMaterialTargetRate(t *testing.T) {
	store := NewRevisionStore()
	recipeID := model.NewID()
	freeze(t, store, recipeWith(recipeID, 1,
		layer(1, "SiO2", 100, 2),
		layer(2, "TiO2", 60, 3)))
	freeze(t, store, recipeWith(recipeID, 2,
		layer(1, "SiO2", 120, 4),
		layer(2, "Ta2O5", 60, 3)))

	report, err := Analyze(store, recipeID, 1, 2, nil)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	counts := kinds(report.Changes)
	if counts[ChangeMaterial] != 1 {
		t.Errorf("material changes = %d, want 1", counts[ChangeMaterial])
	}
	if counts[ChangeTargetThickness] != 1 {
		t.Errorf("target changes = %d, want 1", counts[ChangeTargetThickness])
	}
	if counts[ChangeRate] != 1 {
		t.Errorf("rate changes = %d, want 1", counts[ChangeRate])
	}
	for _, change := range report.Changes {
		if change.Kind == ChangeTargetThickness && change.Layer != 1 {
			t.Errorf("target change anchored to layer %d, want 1", change.Layer)
		}
	}
}

func TestDiffInsertionShiftsOrder(t *testing.T) {
	store := NewRevisionStore()
	recipeID := model.NewID()
	freeze(t, store, recipeWith(recipeID, 1,
		layer(1, "A", 10, 1), layer(2, "B", 10, 1), layer(3, "C", 10, 1)))
	freeze(t, store, recipeWith(recipeID, 2,
		layer(1, "A", 10, 1), layer(2, "X", 10, 1), layer(3, "B", 10, 1), layer(4, "C", 10, 1)))

	report, err := Analyze(store, recipeID, 1, 2, nil)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	counts := kinds(report.Changes)
	if counts[ChangeLayerAdded] != 1 {
		t.Errorf("adds = %d, want 1", counts[ChangeLayerAdded])
	}
	if counts[ChangeLayerReordered] != 2 {
		t.Errorf("reorders = %d, want 2 (B and C shift)", counts[ChangeLayerReordered])
	}
	if counts[ChangeLayerRemoved] != 0 {
		t.Errorf("removes = %d, want 0", counts[ChangeLayerRemoved])
	}
}

func TestDiffLayerRemoval(t *testing.T) {
	store := NewRevisionStore()
	recipeID := model.NewID()
	freeze(t, store, recipeWith(recipeID, 1,
		layer(1, "A", 10, 1), layer(2, "B", 10, 1)))
	freeze(t, store, recipeWith(recipeID, 2, layer(1, "A", 10, 1)))

	report, err := Analyze(store, recipeID, 1, 2, nil)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	if kinds(report.Changes)[ChangeLayerRemoved] != 1 {
		t.Fatalf("want exactly one removed layer, got %v", kinds(report.Changes))
	}
}

func TestEquipmentCommandsFollowRateAndThickness(t *testing.T) {
	store := NewRevisionStore()
	recipeID := model.NewID()
	freeze(t, store, recipeWith(recipeID, 1, layer(1, "SiO2", 100, 2)))
	freeze(t, store, recipeWith(recipeID, 2, layer(1, "SiO2", 120, 4)))

	report, err := Analyze(store, recipeID, 1, 2, nil)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	commandChanges := map[string]Change{}
	for _, change := range report.Changes {
		if change.Kind == ChangeEquipmentCommand {
			commandChanges[change.Command] = change
		}
	}
	// Rate drives source power weight (source.Controller.Layer) and rotation.
	if change, ok := commandChanges[string(model.CommandCondition)]; !ok {
		t.Error("expected condition command change from rate-driven power weight")
	} else if !strings.Contains(change.Detail, "power_weight") {
		t.Errorf("condition detail = %q", change.Detail)
	}
	if _, ok := commandChanges[string(model.CommandRotate)]; !ok {
		t.Error("expected rotate command change from rate-driven profile")
	}
	// Target thickness drives the overcoat shutter limit (interlock.Overcoat).
	closeChange, ok := commandChanges[string(model.CommandCloseShutter)]
	if !ok {
		t.Fatal("expected close_shutter command change from overcoat limit")
	}
	if !strings.Contains(closeChange.Detail, "overcoat_limit_nm") {
		t.Errorf("close detail = %q, want overcoat limit", closeChange.Detail)
	}
	// Pump/vent are run-level and unchanged; they must never appear as changes.
	if _, ok := commandChanges[string(model.CommandPump)]; ok {
		t.Error("pump command should not change between revisions")
	}
	if _, ok := commandChanges[string(model.CommandVent)]; ok {
		t.Error("vent command should not change between revisions")
	}
}

func TestAddedLayerGainsCommandSet(t *testing.T) {
	store := NewRevisionStore()
	recipeID := model.NewID()
	freeze(t, store, recipeWith(recipeID, 1, layer(1, "A", 10, 2)))
	freeze(t, store, recipeWith(recipeID, 2, layer(1, "A", 10, 2), layer(2, "B", 20, 3)))

	report, err := Analyze(store, recipeID, 1, 2, nil)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	gained := map[string]bool{}
	for _, change := range report.Changes {
		if change.Kind == ChangeEquipmentCommand && change.To != nil {
			gained[change.Command] = true
		}
	}
	for _, want := range []model.CommandKind{model.CommandRotate, model.CommandCondition, model.CommandOpenShutter, model.CommandCloseShutter} {
		if !gained[string(want)] {
			t.Errorf("new layer missing gained command %s", want)
		}
	}
}

func TestAggregatedGatesAndInterlocks(t *testing.T) {
	store := NewRevisionStore()
	recipeID := model.NewID()
	freeze(t, store, recipeWith(recipeID, 1, layer(1, "SiO2", 100, 2)))
	freeze(t, store, recipeWith(recipeID, 2, layer(1, "SiO2", 120, 4)))

	report, err := Analyze(store, recipeID, 1, 2, nil)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	gates := map[string]bool{}
	for _, gate := range report.QualityGates {
		gates[gate.Name] = true
	}
	for _, want := range []string{GateThickness, GateLayerEvidence, GateRelease} {
		if !gates[want] {
			t.Errorf("expected quality gate %s", want)
		}
	}
	locks := map[string]bool{}
	for _, value := range report.Interlocks {
		locks[value.Name] = true
	}
	if !locks[InterlockOvercoat] {
		t.Error("thickness change must flag overcoat shutter interlock")
	}
	if !locks[InterlockAdmission] || !locks[InterlockRotation] {
		t.Error("rate change must flag source admission and rotation interlocks")
	}
}

func TestAffectedRunClassification(t *testing.T) {
	recipeID := model.NewID()
	otherRecipe := model.NewID()
	runs := []*model.Run{
		{ID: model.NewID(), Batch: "active-old", RecipeID: recipeID, RecipeRevision: 1, State: model.Coating},
		{ID: model.NewID(), Batch: "active-new", RecipeID: recipeID, RecipeRevision: 2, State: model.Coating},
		{ID: model.NewID(), Batch: "done-old", RecipeID: recipeID, RecipeRevision: 1, State: model.Released},
		{ID: model.NewID(), Batch: "unrecorded", RecipeID: recipeID, RecipeRevision: 0, State: model.Pumping},
		{ID: model.NewID(), Batch: "other", RecipeID: otherRecipe, RecipeRevision: 1, State: model.Coating},
	}
	store := NewRevisionStore()
	freeze(t, store, recipeWith(recipeID, 1, layer(1, "A", 10, 2)))
	freeze(t, store, recipeWith(recipeID, 2, layer(1, "A", 12, 2)))

	report, err := Analyze(store, recipeID, 1, 2, runs)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	byBatch := map[string]AffectedRun{}
	for _, entry := range report.Runs {
		byBatch[entry.Batch] = entry
	}
	if len(byBatch) != 4 {
		t.Fatalf("evaluated %d runs for recipe, want 4 (other recipe excluded)", len(byBatch))
	}
	if !byBatch["active-old"].Affected {
		t.Error("active run on old revision must be affected")
	}
	if byBatch["active-new"].Affected {
		t.Error("run already on new revision must not be affected")
	}
	if byBatch["done-old"].Affected {
		t.Error("released run keeps its evidence baseline and must not be affected")
	}
	if !byBatch["unrecorded"].Affected {
		t.Error("active run with unrecorded revision must be fail-safe classified affected")
	}
}

func TestReportIsAuditableAndStable(t *testing.T) {
	store := NewRevisionStore()
	recipeID := model.NewID()
	freeze(t, store, recipeWith(recipeID, 1, layer(1, "A", 10, 2)))
	freeze(t, store, recipeWith(recipeID, 2, layer(1, "A", 12, 2)))

	first, err := Analyze(store, recipeID, 1, 2, nil)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	if err := first.Validate(); err != nil {
		t.Fatalf("report validation: %v", err)
	}
	if first.Basis != BasisFrozenRevisions {
		t.Fatalf("basis = %q, want %q", first.Basis, BasisFrozenRevisions)
	}
	if first.FromRevision.Fingerprint == "" || first.ToRevision.Fingerprint == "" || first.Fingerprint == "" {
		t.Fatal("fingerprints must be populated")
	}
	if first.FromRevision.Number != 1 || first.ToRevision.Number != 2 {
		t.Fatalf("revision refs = %d -> %d", first.FromRevision.Number, first.ToRevision.Number)
	}
	// Reversing the argument order yields the same low->high comparison.
	reversed, err := Analyze(store, recipeID, 2, 1, nil)
	if err != nil {
		t.Fatalf("reversed analyze: %v", err)
	}
	if reversed.FromRevision.Number != 1 || reversed.ToRevision.Number != 2 {
		t.Fatal("reversed arguments should normalize to ascending revisions")
	}
	if reversed.Fingerprint != first.Fingerprint {
		t.Fatalf("fingerprint not stable: %s != %s", reversed.Fingerprint, first.Fingerprint)
	}
	rendered := first.RenderText()
	for _, want := range []string{"AUDIT REPORT", "frozen_revisions", "fingerprint:", "AFFECTED RUNS", "SAFETY INTERLOCKS", "revision=1", "revision=2"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("audit text missing %q", want)
		}
	}
}
