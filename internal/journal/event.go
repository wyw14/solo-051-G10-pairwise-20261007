package journal

import "github.com/wyw14/cry-161/internal/model"

func RunStarted(run model.Run) model.Event {
	return model.NewEvent("run.started", run.ID, map[string]any{"batch": run.Batch, "state": run.State})
}
func RunStateChanged(run model.Run, previous model.RunState) model.Event {
	return model.NewEvent("run.state", run.ID, map[string]any{"from": previous, "to": run.State})
}
func IncidentRaised(incident model.Incident) model.Event {
	return model.NewEvent("incident", incident.RunID, map[string]any{"severity": incident.Severity, "message": incident.Message})
}
func RecipeRevisionFrozen(recipeID model.ID, number int, revisionID model.ID) model.Event {
	return model.NewEvent("recipe.revision_frozen", recipeID, map[string]any{"revision": number, "revision_id": revisionID})
}
func RecipeImpactAnalyzed(recipeID model.ID, fromNumber, toNumber int, fingerprint string, changeCount int) model.Event {
	return model.NewEvent("recipe.impact_analyzed", recipeID, map[string]any{"from_revision": fromNumber, "to_revision": toNumber, "basis": "frozen_revisions", "fingerprint": fingerprint, "changes": changeCount})
}
