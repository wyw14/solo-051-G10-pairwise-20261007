package service

import (
	"context"
	"errors"
	"testing"

	"github.com/wyw14/cry-161/internal/model"
	"github.com/wyw14/cry-161/internal/recipe"
)

func seededRecipe(t *testing.T, svc *Service) *model.Recipe {
	t.Helper()
	list := svc.RecipesList()
	if len(list) != 1 {
		t.Fatalf("seed recipes = %d, want 1", len(list))
	}
	return list[0]
}

func TestStartStampsFrozenRevision(t *testing.T) {
	svc := New()
	seed := seededRecipe(t, svc)

	runValue, err := svc.Start(context.Background(), "batch-1", seed.ID)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if runValue.RecipeRevision != 1 {
		t.Fatalf("run stamped revision %d, want frozen rev 1", runValue.RecipeRevision)
	}
}

func TestDraftIsNotAReleasedChange(t *testing.T) {
	svc := New()
	seed := seededRecipe(t, svc)

	edited := *seed
	edited.Layers[1] = model.Layer{Number: 1, Material: "SiO2", TargetNanometers: 999, RateNanometers: 9}
	draft, err := svc.SaveDraft(&edited)
	if err != nil {
		t.Fatalf("save draft: %v", err)
	}
	if draft.Published {
		t.Fatal("draft must not be published")
	}

	// Only rev 1 is frozen; the draft revision number must not resolve.
	if _, err := svc.AnalyzeImpact(seed.ID, 1, edited.Revision); !errors.As(err, new(recipe.ErrUnfrozenRevision)) {
		t.Fatalf("analyze against draft revision: expected ErrUnfrozenRevision, got %v", err)
	}
	// There is still only one frozen revision, so no released comparison exists.
	if _, err := svc.AnalyzeImpact(seed.ID, 0, 0); err == nil {
		t.Fatal("expected impact analysis to refuse with a single frozen revision even while a draft exists")
	}
	if frozen := svc.ListRevisions(seed.ID); len(frozen) != 1 {
		t.Fatalf("frozen revisions = %d, draft must not freeze one", len(frozen))
	}
}

func TestReleaseFreezesAndImpactClassifiesRuns(t *testing.T) {
	svc := New()
	seed := seededRecipe(t, svc)
	ctx := context.Background()

	// Active run starts on rev 1.
	activeRun, err := svc.Start(ctx, "active", seed.ID)
	if err != nil {
		t.Fatalf("start active: %v", err)
	}
	// A previously completed run also on rev 1 must keep its evidence baseline.
	doneRun := &model.Run{ID: model.NewID(), Batch: "done", RecipeID: seed.ID, RecipeRevision: 1, State: model.Pumping, CreatedAt: activeRun.CreatedAt, UpdatedAt: activeRun.UpdatedAt}
	svc.Runs.Add(doneRun)
	for _, next := range []model.RunState{model.Conditioning, model.Coating, model.Cooling, model.Venting, model.Released} {
		if !svc.Runs.Transition(doneRun.ID, next) {
			t.Fatalf("advance done run to %s", next)
		}
	}

	// Release rev 2: thicker target and higher rate on layer 1.
	edited := *seed
	edited.Layers[1] = model.Layer{Number: 1, Material: "SiO2", TargetNanometers: 140, RateNanometers: 5}
	released, err := svc.ReleaseRecipe(&edited)
	if err != nil {
		t.Fatalf("release: %v", err)
	}
	if released.Number != 2 {
		t.Fatalf("released revision = %d, want 2", released.Number)
	}
	if active := svc.RecipesList()[0]; active.Revision != 2 {
		t.Fatalf("active recipe revision = %d, want 2", active.Revision)
	}

	report, err := svc.AnalyzeImpact(seed.ID, 0, 0)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	if report.FromRevision.Number != 1 || report.ToRevision.Number != 2 {
		t.Fatalf("report window = %d -> %d", report.FromRevision.Number, report.ToRevision.Number)
	}
	if report.Basis != recipe.BasisFrozenRevisions {
		t.Fatalf("basis = %q", report.Basis)
	}
	if len(report.Changes) == 0 {
		t.Fatal("expected material changes between frozen revisions")
	}
	byRun := map[model.ID]recipe.AffectedRun{}
	for _, entry := range report.Runs {
		byRun[entry.RunID] = entry
	}
	active, ok := byRun[activeRun.ID]
	if !ok || !active.Affected {
		t.Fatal("active run on rev 1 must be affected by rev 2")
	}
	done, ok := byRun[doneRun.ID]
	if !ok || done.Affected {
		t.Fatal("released run must not be treated as affected")
	}
	foundOvercoat, foundAdmission := false, false
	for _, value := range report.Interlocks {
		if value.Name == recipe.InterlockOvercoat {
			foundOvercoat = true
		}
		if value.Name == recipe.InterlockAdmission {
			foundAdmission = true
		}
	}
	if !foundOvercoat || !foundAdmission {
		t.Fatalf("expected overcoat and admission interlocks, got %+v", report.Interlocks)
	}

	// A run started after release executes rev 2 and is not affected.
	svc.Carrier.Unload("active")
	newRun, err := svc.Start(ctx, "new", seed.ID)
	if err != nil {
		t.Fatalf("start new: %v", err)
	}
	if newRun.RecipeRevision != 2 {
		t.Fatalf("post-release run stamped %d, want 2", newRun.RecipeRevision)
	}
	later, err := svc.AnalyzeImpact(seed.ID, 1, 2)
	if err != nil {
		t.Fatalf("re-analyze: %v", err)
	}
	for _, entry := range later.Runs {
		if entry.RunID == newRun.ID && entry.Affected {
			t.Fatal("run already executing rev 2 must not be affected")
		}
	}
}

func TestDraftIgnoredIsRecordedInReport(t *testing.T) {
	svc := New()
	seed := seededRecipe(t, svc)

	edited := *seed
	edited.Layers[1] = model.Layer{Number: 1, Material: "SiO2", TargetNanometers: 140, RateNanometers: 5}
	if _, err := svc.SaveDraft(&edited); err != nil {
		t.Fatalf("save draft: %v", err)
	}
	if _, err := svc.ReleaseRecipe(&edited); err != nil {
		t.Fatalf("release: %v", err)
	}

	report, err := svc.AnalyzeImpact(seed.ID, 1, 2)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	if report.DraftsIgnored != 1 {
		t.Fatalf("drafts_ignored = %d, want 1", report.DraftsIgnored)
	}
}
