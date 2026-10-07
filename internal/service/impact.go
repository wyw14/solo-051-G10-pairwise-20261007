package service

import (
	"fmt"
	"github.com/wyw14/cry-161/internal/impact"
	"github.com/wyw14/cry-161/internal/model"
	"github.com/wyw14/cry-161/internal/recipe"
)

// ImpactRequest is the service-level analysis input. Revision numbers of 0
// select the newest frozen revision on that side.
type ImpactRequest struct {
	RecipeID    model.ID
	FromNumber  int
	ToNumber    int
	GeneratedBy string
}

// FreezeRecipe snapshots the currently PUBLISHED active recipe into an
// immutable frozen revision. Draft edits are never frozen here.
func (s *Service) FreezeRecipe(recipeID model.ID) (recipe.Revision, error) {
	active, err := s.Recipes.Active(recipeID)
	if err != nil {
		return recipe.Revision{}, fmt.Errorf("cannot freeze: %w", err)
	}
	return s.Revisions.Freeze(active)
}

// FrozenRevisions lists the real frozen revisions of a recipe in number order.
func (s *Service) FrozenRevisions(recipeID model.ID) []recipe.Revision {
	return s.Revisions.List(recipeID)
}

// AnalyzeImpact compares two frozen revisions, derives the operational impact
// and persists an auditable report. Draft differences are intentionally out of
// scope: only frozen revisions are resolved, and any draft ids are recorded in
// the report's exclusion section.
func (s *Service) AnalyzeImpact(req ImpactRequest) (impact.Report, error) {
	if req.RecipeID == "" {
		return impact.Report{}, fmt.Errorf("recipe id is required")
	}
	runs := s.Runs.List()
	report, err := impact.Analyze(s.Revisions, s.RunBindings, runs, s.Drafts.ListIDs(), impact.Request{
		RecipeID:    req.RecipeID,
		FromNumber:  req.FromNumber,
		ToNumber:    req.ToNumber,
		GeneratedBy: req.GeneratedBy,
	})
	if err != nil {
		return impact.Report{}, err
	}
	if err := s.ImpactReports.Save(report); err != nil {
		return impact.Report{}, err
	}
	return report, nil
}

func (s *Service) ListImpactReports(recipeID model.ID) ([]impact.Report, error) {
	return s.ImpactReports.List(recipeID)
}

func (s *Service) GetImpactReport(id model.ID) (impact.Report, bool, error) {
	return s.ImpactReports.Get(id)
}

// BindRunToFrozen lets an operator explicitly attribute an existing run to a
// specific frozen revision. The revision must be real and frozen.
func (s *Service) BindRunToFrozen(runID model.ID, revisionNumber int, source impact.BindingSource) (impact.RunBinding, error) {
	value, ok := s.Runs.Get(runID)
	if !ok {
		return impact.RunBinding{}, fmt.Errorf("run not found")
	}
	if _, err := impact.ResolveFrozen(s.Revisions, value.RecipeID, revisionNumber); err != nil {
		return impact.RunBinding{}, err
	}
	return s.RunBindings.Bind(runID, value.RecipeID, revisionNumber, source)
}
