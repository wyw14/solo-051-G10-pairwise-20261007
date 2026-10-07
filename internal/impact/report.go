package impact

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/wyw14/cry-161/internal/model"
	"sort"
	"strings"
	"time"
)

// RevisionProvenance proves that one side of the comparison is a real frozen
// revision: it carries the revision identity and a content fingerprint of the
// frozen layers, so the report can be re-verified against the revision store
// long after the analysis ran.
type RevisionProvenance struct {
	RecipeID    model.ID  `json:"recipe_id"`
	RevisionID  model.ID  `json:"revision_id"`
	Number      int       `json:"number"`
	FrozenAt    time.Time `json:"frozen_at"`
	LayerCount  int       `json:"layer_count"`
	Fingerprint string    `json:"fingerprint_sha256"`
	Source      string    `json:"source"` // always "frozen_revision_store"
}

// DraftExclusion documents the mutable inputs that were deliberately NOT used.
// An impact report only cites published (frozen) state; listing the exclusions
// makes that guarantee auditable instead of implicit.
type DraftExclusion struct {
	DraftRecipeIDs []model.ID `json:"draft_recipe_ids"`
	Note           string     `json:"note"`
}

// UnboundRun is a run of the recipe that could not be attributed because it has
// no frozen-revision binding. It is surfaced, never guessed at.
type UnboundRun struct {
	RunID model.ID       `json:"run_id"`
	Batch string         `json:"batch"`
	State model.RunState `json:"state"`
	Note  string         `json:"note"`
}

// OutOfWindowRun is a run that is properly bound, but to a frozen revision
// other than the comparison baseline, so it is not in the blast radius.
type OutOfWindowRun struct {
	RunID         model.ID       `json:"run_id"`
	Batch         string         `json:"batch"`
	State         model.RunState `json:"state"`
	BoundRevision int            `json:"bound_revision"`
	Note          string         `json:"note"`
}

// Report is the auditable artifact of one frozen-pair comparison.
type Report struct {
	ID          model.ID           `json:"id"`
	Kind        string             `json:"kind"`
	GeneratedAt time.Time          `json:"generated_at"`
	GeneratedBy string             `json:"generated_by"`
	From        RevisionProvenance `json:"from"`
	To          RevisionProvenance `json:"to"`
	Drafts      DraftExclusion     `json:"draft_exclusion"`
	Unbound     []UnboundRun       `json:"unbound_runs"`
	OutOfWindow []OutOfWindowRun   `json:"out_of_window_runs"`
	Diff        Diff               `json:"diff"`
	Impact      Assessment         `json:"impact"`
}

func provenance(rev frozenRevision) RevisionProvenance {
	return RevisionProvenance{
		RecipeID:    rev.recipeID,
		RevisionID:  rev.id,
		Number:      rev.number,
		FrozenAt:    rev.frozenAt,
		LayerCount:  len(rev.layers),
		Fingerprint: fingerprint(rev.layers),
		Source:      "frozen_revision_store",
	}
}

// frozenRevision is the immutable snapshot the analyzer hands to the report.
type frozenRevision struct {
	id       model.ID
	recipeID model.ID
	number   int
	frozenAt time.Time
	layers   []model.Layer
}

// fingerprint hashes the frozen layers in canonical form. Field order is fixed
// explicitly so the value does not depend on map iteration.
func fingerprint(layers []model.Layer) string {
	type canonicalLayer struct {
		Number   int     `json:"number"`
		Material string  `json:"material"`
		TargetNm float64 `json:"target_nm"`
		RateNmS  float64 `json:"rate_nm_s"`
	}
	ordered := sortedLayers(layers)
	rows := make([]canonicalLayer, 0, len(ordered))
	for _, layer := range ordered {
		rows = append(rows, canonicalLayer{Number: layer.Number, Material: layer.Material, TargetNm: layer.TargetNanometers, RateNmS: layer.RateNanometers})
	}
	data, _ := json.Marshal(rows)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// BuildInputs carries everything BuildReport needs; all revision data must have
// come through ResolveFrozen (frozenRevision is unexported and produced only by
// the analyzer in this package).
type BuildInputs struct {
	GeneratedBy string
	From        frozenRevision
	To          frozenRevision
	DraftIDs    []model.ID
	Runs        []*model.Run
	Bindings    map[model.ID]RunBinding
}

func BuildReport(in BuildInputs) Report {
	fromView := RevisionView{RecipeID: in.From.recipeID, Number: in.From.number, Layers: in.From.layers}
	toView := RevisionView{RecipeID: in.To.recipeID, Number: in.To.number, Layers: in.To.layers}
	diff := ComputeDiff(fromView, toView)
	impact := Assess(diff, in.Runs, in.Bindings)
	unbound := []UnboundRun{}
	outOfWindow := []OutOfWindowRun{}
	for _, r := range in.Runs {
		if r.RecipeID != "" && r.RecipeID != in.From.recipeID {
			continue // different recipe entirely; not part of this comparison
		}
		binding, isBound := in.Bindings[r.ID]
		if !isBound {
			unbound = append(unbound, UnboundRun{RunID: r.ID, Batch: r.Batch, State: r.State, Note: "no frozen-revision binding exists; run excluded from impact and requires manual attribution"})
			continue
		}
		if binding.RecipeID != in.From.recipeID || binding.RevisionNumber != in.From.number {
			outOfWindow = append(outOfWindow, OutOfWindowRun{
				RunID:         r.ID,
				Batch:         r.Batch,
				State:         r.State,
				BoundRevision: binding.RevisionNumber,
				Note:          fmt.Sprintf("bound to frozen revision %d; outside the %d -> %d window", binding.RevisionNumber, in.From.number, in.To.number),
			})
		}
	}
	sort.SliceStable(unbound, func(i, j int) bool { return unbound[i].RunID < unbound[j].RunID })
	sort.SliceStable(outOfWindow, func(i, j int) bool { return outOfWindow[i].RunID < outOfWindow[j].RunID })
	draftIDs := append([]model.ID(nil), in.DraftIDs...)
	sort.SliceStable(draftIDs, func(i, j int) bool { return draftIDs[i] < draftIDs[j] })
	return Report{
		ID:          model.NewID(),
		Kind:        "recipe_impact_analysis",
		GeneratedAt: time.Now().UTC(),
		GeneratedBy: operator(in.GeneratedBy),
		From:        provenance(in.From),
		To:          provenance(in.To),
		Drafts: DraftExclusion{
			DraftRecipeIDs: draftIDs,
			Note:           "drafts and mutable active-recipe edits are not frozen revisions; their differences were not compared and are not represented as published changes",
		},
		Unbound:     unbound,
		OutOfWindow: outOfWindow,
		Diff:        diff,
		Impact:      impact,
	}
}

func operator(value string) string {
	if strings.TrimSpace(value) == "" {
		return "system"
	}
	return value
}

// Validate enforces the audit invariant: both sides must cite real frozen
// revisions with matching fingerprints, the pair must be ascending, and the
// report must not claim changes it cannot attribute.
func (r Report) Validate() error {
	if r.ID == "" {
		return fmt.Errorf("report identity is required")
	}
	if r.Kind != "recipe_impact_analysis" {
		return fmt.Errorf("unexpected report kind %q", r.Kind)
	}
	if r.GeneratedAt.IsZero() || r.GeneratedBy == "" {
		return fmt.Errorf("report audit metadata is incomplete")
	}
	if r.From.Source != "frozen_revision_store" || r.To.Source != "frozen_revision_store" {
		return fmt.Errorf("report may only cite frozen revisions")
	}
	if r.From.RecipeID == "" || r.To.RecipeID == "" || r.From.RevisionID == "" || r.To.RevisionID == "" {
		return fmt.Errorf("revision provenance identity is required")
	}
	if r.From.RecipeID != r.To.RecipeID {
		return fmt.Errorf("revisions belong to different recipes")
	}
	if r.From.Number >= r.To.Number {
		return fmt.Errorf("report pair must be ascending frozen revisions")
	}
	if r.From.Fingerprint == "" || r.To.Fingerprint == "" {
		return fmt.Errorf("revision fingerprints are required")
	}
	if r.From.LayerCount != r.Diff.LayerCountBefore || r.To.LayerCount != r.Diff.LayerCountAfter {
		return fmt.Errorf("report layer counts do not match provenance")
	}
	return nil
}

// RenderText produces the human-auditable, line-oriented version of the report.
func (r Report) RenderText() string {
	var b strings.Builder
	line := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }
	line("RECIPE IMPACT ANALYSIS REPORT")
	line("report_id: %s", r.ID)
	line("generated_at: %s", r.GeneratedAt.Format(time.RFC3339))
	line("generated_by: %s", r.GeneratedBy)
	line("")
	line("BASELINE (frozen)")
	lineProvenance(&b, "  ", r.From)
	line("TARGET (frozen)")
	lineProvenance(&b, "  ", r.To)
	line("")
	line("PUBLISHED-CHANGE SCOPE: %s", r.Diff.String())
	line("dimensions: %s", joinDimensions(r.Diff.Dimensions))
	for _, change := range r.Diff.Changes {
		line("  - layer %d %s material=%s before_pos=%d after_pos=%d", change.Layer, change.Type, change.Material, change.BeforeLayer, change.AfterLayer)
		for _, field := range change.Fields {
			if field.OldValue != "" {
				line("      %s: %s -> %s", field.Field, field.OldValue, field.NewValue)
			} else {
				line("      %s: %.4f -> %.4f", field.Field, field.Before, field.After)
			}
		}
		for _, command := range change.CommandDelta {
			line("      command %s@%d %s (%s -> %s)", command.Kind, command.Layer, command.Type, command.Before, command.After)
		}
	}
	line("")
	line("AFFECTED RUNS: %d", len(r.Impact.AffectedRuns))
	for _, affected := range r.Impact.AffectedRuns {
		line("  - run %s batch=%q state=%s bound_rev=%d exposure=%s changed_layers=%v", affected.RunID, affected.Batch, affected.State, affected.BoundRevision, affected.Exposure, affected.ChangedLayers)
		for _, reason := range affected.Reasons {
			line("      reason: %s", reason)
		}
	}
	line("QUALITY GATES TO RE-EVALUATE: %d", len(r.Impact.QualityGates))
	for _, gate := range r.Impact.QualityGates {
		line("  - %s severity=%s layers=%v", gate.Gate, gate.Severity, gate.Layers)
		line("      %s", gate.Reason)
	}
	line("SAFETY INTERLOCKS TO RE-CONFIRM: %d", len(r.Impact.Interlocks))
	for _, interlock := range r.Impact.Interlocks {
		line("  - %s severity=%s layers=%v", interlock.Interlock, interlock.Severity, interlock.Layers)
		line("      %s", interlock.Reason)
	}
	line("")
	line("DRAFT EXCLUSION: %s", r.Drafts.Note)
	if len(r.Drafts.DraftRecipeIDs) > 0 {
		line("  draft recipe ids not compared: %v", r.Drafts.DraftRecipeIDs)
	}
	line("UNBOUND RUNS (not attributed): %d", len(r.Unbound))
	for _, entry := range r.Unbound {
		line("  - run %s batch=%q state=%s: %s", entry.RunID, entry.Batch, entry.State, entry.Note)
	}
	line("OUT-OF-WINDOW RUNS (bound to other frozen revisions): %d", len(r.OutOfWindow))
	for _, entry := range r.OutOfWindow {
		line("  - run %s batch=%q state=%s bound_rev=%d: %s", entry.RunID, entry.Batch, entry.State, entry.BoundRevision, entry.Note)
	}
	line("")
	line("END OF REPORT %s", r.ID)
	return b.String()
}

func lineProvenance(b *strings.Builder, indent string, p RevisionProvenance) {
	fmt.Fprintf(b, "%srecipe_id: %s\n", indent, p.RecipeID)
	fmt.Fprintf(b, "%srevision_id: %s number: %d frozen_at: %s\n", indent, p.RevisionID, p.Number, p.FrozenAt.Format(time.RFC3339))
	fmt.Fprintf(b, "%slayers: %d sha256: %s\n", indent, p.LayerCount, p.Fingerprint)
	fmt.Fprintf(b, "%ssource: %s\n", indent, p.Source)
}

func joinDimensions(dimensions []Dimension) string {
	values := make([]string, 0, len(dimensions))
	for _, d := range dimensions {
		values = append(values, string(d))
	}
	return strings.Join(values, ",")
}
