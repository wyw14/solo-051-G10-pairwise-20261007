package impact

import (
	"github.com/wyw14/cry-161/internal/model"
	"github.com/wyw14/cry-161/internal/recipe"
)

// Request is one impact-analysis request. FromNumber/ToNumber == 0 select the
// newest frozen revision on that side.
type Request struct {
	RecipeID    model.ID
	FromNumber  int
	ToNumber    int
	GeneratedBy string
}

func frozenFromRevision(rev recipe.Revision) frozenRevision {
	return frozenRevision{id: rev.ID, recipeID: rev.RecipeID, number: rev.Number, frozenAt: rev.CreatedAt, layers: rev.Layers}
}

// Analyze resolves two REAL frozen revisions, computes the diff and impact,
// validates the audit invariants and returns the report. Draft state is never
// read for the comparison; draftIDs are only listed in the exclusion section.
func Analyze(store *recipe.RevisionStore, binder *Binder, runs []*model.Run, draftIDs []model.ID, req Request) (Report, error) {
	fromRev, toRev, err := ResolvePair(store, req.RecipeID, req.FromNumber, req.ToNumber)
	if err != nil {
		return Report{}, err
	}
	bindings := map[model.ID]RunBinding{}
	if binder != nil {
		for _, binding := range binder.ForRecipe(req.RecipeID) {
			bindings[binding.RunID] = binding
		}
	}
	report := BuildReport(BuildInputs{
		GeneratedBy: req.GeneratedBy,
		From:        frozenFromRevision(fromRev),
		To:          frozenFromRevision(toRev),
		DraftIDs:    draftIDs,
		Runs:        runs,
		Bindings:    bindings,
	})
	if err := report.Validate(); err != nil {
		return Report{}, err
	}
	return report, nil
}

// BindIfFrozen attributes a run to a revision only when that revision number
// actually exists in the frozen store. A mutable active recipe is not enough:
// such a run deliberately stays unbound and is surfaced by future reports.
func BindIfFrozen(store *recipe.RevisionStore, binder *Binder, run model.Run, source BindingSource) (bool, error) {
	if store == nil || binder == nil {
		return false, nil
	}
	revision, err := ResolveFrozen(store, run.RecipeID, LatestRevision)
	if err != nil {
		return false, nil // no frozen baseline yet; run remains honestly unbound
	}
	if _, err := binder.Bind(run.ID, run.RecipeID, revision.Number, source); err != nil {
		return false, err
	}
	return true, nil
}
