package service

import (
	"fmt"

	"github.com/wyw14/cry-161/internal/journal"
	"github.com/wyw14/cry-161/internal/model"
	"github.com/wyw14/cry-161/internal/recipe"
)

// ListRevisions returns only frozen, released revisions for a recipe. Drafts
// are intentionally absent: an impact report may cite just these versions.
func (s *Service) ListRevisions(recipeID model.ID) []recipe.Revision {
	return s.Revisions.List(recipeID)
}

// SaveDraft records an unpublished recipe edit. It never freezes a revision,
// so a draft cannot be cited as a released change by AnalyzeImpact.
func (s *Service) SaveDraft(edited *model.Recipe) (*model.Recipe, error) {
	active, err := s.Recipes.Active(edited.ID)
	if err != nil {
		return nil, err
	}
	edited.Name = active.Name
	// A draft is the candidate for the next revision number, but it is not
	// frozen or published, so that number does not resolve in RevisionStore.
	edited.Revision = active.Revision + 1
	return s.Drafts.Save(edited), nil
}

// ReleaseRecipe publishes an edited recipe by freezing it as the next
// immutable revision. Only a revision produced here can be compared by
// AnalyzeImpact; drafts are promoted explicitly, never by accident.
func (s *Service) ReleaseRecipe(edited *model.Recipe) (recipe.Revision, error) {
	if edited == nil {
		return recipe.Revision{}, fmt.Errorf("recipe is required")
	}
	active, err := s.Recipes.Active(edited.ID)
	if err != nil {
		return recipe.Revision{}, err
	}
	edited.Name = active.Name
	edited.Revision = active.Revision + 1
	edited.Published = true
	frozen, err := s.Revisions.Freeze(edited)
	if err != nil {
		return recipe.Revision{}, fmt.Errorf("freeze rejected: %w", err)
	}
	s.Recipes.Publish(edited)
	_ = s.Journal.Append(journal.RecipeRevisionFrozen(edited.ID, frozen.Number, frozen.ID))
	return frozen, nil
}

// AnalyzeImpact compares two frozen revisions of one recipe and computes the
// affected runs, quality gates and safety interlocks. from/to are frozen
// revision numbers; a zero value selects the two most recent frozen revisions.
// Draft revisions are not resolvable here and fail closed.
func (s *Service) AnalyzeImpact(recipeID model.ID, fromNumber, toNumber int) (recipe.Report, error) {
	frozen := s.Revisions.List(recipeID)
	if len(frozen) == 0 {
		return recipe.Report{}, fmt.Errorf("recipe has no frozen released revisions")
	}
	if fromNumber == 0 || toNumber == 0 {
		if len(frozen) < 2 {
			return recipe.Report{}, fmt.Errorf("recipe has only one frozen revision; release a new version before analyzing impact")
		}
		if fromNumber == 0 {
			fromNumber = frozen[len(frozen)-2].Number
		}
		if toNumber == 0 {
			toNumber = frozen[len(frozen)-1].Number
		}
	}
	report, err := recipe.Analyze(s.Revisions, recipeID, fromNumber, toNumber, s.Runs.List())
	if err != nil {
		return recipe.Report{}, err
	}
	if _, exists := s.Drafts.Get(recipeID); exists {
		report.DraftsIgnored = 1
	}
	if err := report.Validate(); err != nil {
		return recipe.Report{}, err
	}
	_ = s.Journal.Append(journal.RecipeImpactAnalyzed(recipeID, report.FromRevision.Number, report.ToRevision.Number, report.Fingerprint, len(report.Changes)))
	return report, nil
}
