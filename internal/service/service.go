package service

import (
	"context"
	"fmt"
	"github.com/wyw14/cry-161/internal/chamber"
	"github.com/wyw14/cry-161/internal/journal"
	"github.com/wyw14/cry-161/internal/model"
	"github.com/wyw14/cry-161/internal/recipe"
	"github.com/wyw14/cry-161/internal/rotation"
	"github.com/wyw14/cry-161/internal/run"
	"github.com/wyw14/cry-161/internal/source"
	"github.com/wyw14/cry-161/internal/substrate"
	"github.com/wyw14/cry-161/internal/vacuum"
	"os"
)

type Service struct {
	Runs      *run.Registry
	Recipes   *recipe.Store
	Drafts    *recipe.DraftBook
	Revisions *recipe.RevisionStore
	Carrier   *substrate.Carrier
	Chamber   *chamber.Chamber
	Vacuum    *vacuum.Controller
	Source    *source.Controller
	Rotation  *rotation.Controller
	Journal   *journal.Store
}

func New() *Service {
	recipes := recipe.NewStore()
	id := model.NewID()
	seed := &model.Recipe{ID: id, Name: "AR coating", Revision: 1, Published: true, Layers: map[int]model.Layer{1: {Number: 1, Material: "SiO2", TargetNanometers: 100, RateNanometers: 2}, 2: {Number: 2, Material: "TiO2", TargetNanometers: 60, RateNanometers: 3}}}
	recipes.Publish(seed)
	revisions := recipe.NewRevisionStore()
	dir := os.Getenv("OPTICCOAT_DATA")
	if dir == "" {
		dir = "data"
	}
	svc := &Service{Runs: run.NewRegistry(), Recipes: recipes, Drafts: recipe.NewDraftBook(), Revisions: revisions, Carrier: substrate.NewCarrier(), Chamber: chamber.New("coat-01"), Vacuum: vacuum.NewController(), Source: source.NewController(100), Rotation: rotation.NewController(), Journal: journal.New(dir)}
	if frozen, err := revisions.Freeze(seed); err == nil {
		_ = svc.Journal.Append(journal.RecipeRevisionFrozen(seed.ID, frozen.Number, frozen.ID))
	}
	return svc
}
func (s *Service) RecipesList() []*model.Recipe { return s.Recipes.List() }
func (s *Service) RunsList() []*model.Run       { return s.Runs.List() }
func (s *Service) Start(ctx context.Context, batch string, recipeID model.ID) (*model.Run, error) {
	coordinator := &run.Coordinator{Registry: s.Runs, Recipes: s.Recipes, Revisions: s.Revisions, Carrier: s.Carrier, Chamber: s.Chamber, Vacuum: s.Vacuum, Journal: s.Journal}
	return coordinator.Start(ctx, batch, recipeID)
}
func (s *Service) UpdateDraft(id model.ID, layer model.Layer) error {
	if !s.Drafts.UpdateLayer(id, layer) {
		return fmt.Errorf("draft not found")
	}
	return nil
}
