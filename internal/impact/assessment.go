package impact

import (
	"fmt"
	"github.com/wyw14/cry-161/internal/model"
	"sort"
)

// Exposure classifies how a bound run meets the revision delta.
type Exposure string

const (
	ExposureCompleted Exposure = "completed" // run already finished on the old revision
	ExposureInFlight  Exposure = "in_flight" // run is active inside the chamber
	ExposureFailed    Exposure = "failed"    // run failed on the old revision
	ExposureUpcoming  Exposure = "upcoming"  // bound run not yet started
)

// AffectedRun is one run whose frozen baseline is the "from" revision and that
// therefore inherits the delta.
type AffectedRun struct {
	RunID         model.ID       `json:"run_id"`
	Batch         string         `json:"batch"`
	State         model.RunState `json:"state"`
	BoundRevision int            `json:"bound_revision"`
	Exposure      Exposure       `json:"exposure"`
	CurrentLayer  int            `json:"current_layer,omitempty"`
	ChangedLayers []int          `json:"changed_layers"`
	Reasons       []string       `json:"reasons"`
}

// GateName identifies one of the quality gates that actually exists in the
// release path (model.EvaluateRelease / run.ReleaseQuality).
type GateName string

const (
	GateRunState       GateName = "run_state"
	GateLayerEvidence  GateName = "layer_evidence"
	GateIncidents      GateName = "incidents"
	GateOpticalRelease GateName = "optical_release_quality"
)

// GateImpact states that one release gate must be re-evaluated because of the
// named layers.
type GateImpact struct {
	Gate     GateName `json:"gate"`
	Layers   []int    `json:"layers"`
	Severity string   `json:"severity"` // info, warning, critical
	Reason   string   `json:"reason"`
}

// InterlockName identifies one of the safety interlocks the runtime actually
// enforces.
type InterlockName string

const (
	InterlockVacuumPermit InterlockName = "vacuum_deposition_permit"
	InterlockOvercoat     InterlockName = "overcoat_shutter_cutoff"
	InterlockChamberReady InterlockName = "chamber_ready_cooldown"
	InterlockRotation     InterlockName = "rotation_profile"
)

// InterlockImpact states that one safety interlock must be re-armed or
// re-confirmed because of the named layers.
type InterlockImpact struct {
	Interlock InterlockName `json:"interlock"`
	Layers    []int         `json:"layers"`
	Severity  string        `json:"severity"`
	Reason    string        `json:"reason"`
}

// Assessment is the derived operational impact of a frozen diff.
type Assessment struct {
	AffectedRuns []AffectedRun     `json:"affected_runs"`
	QualityGates []GateImpact      `json:"quality_gates"`
	Interlocks   []InterlockImpact `json:"interlocks"`
}

func exposureFor(state model.RunState) Exposure {
	switch state {
	case model.Released:
		return ExposureCompleted
	case model.Failed:
		return ExposureFailed
	case model.Loading, model.Pumping, model.Conditioning, model.Coating, model.Cooling, model.Venting:
		return ExposureInFlight
	default:
		return ExposureUpcoming
	}
}

// changedLayersByNumber flattens the diff to layer-number -> set.
func changedLayers(diff Diff) map[int]bool {
	out := map[int]bool{}
	for _, change := range diff.Changes {
		out[change.Layer] = true
		if change.BeforeLayer > 0 {
			out[change.BeforeLayer] = true
		}
	}
	return out
}

func sortedLayersSet(set map[int]bool) []int {
	out := make([]int, 0, len(set))
	for number := range set {
		out = append(out, number)
	}
	sort.Ints(out)
	return out
}

// fieldLayers returns the layer numbers carrying a change in one field.
func fieldLayers(diff Diff, field string) []int {
	set := map[int]bool{}
	for _, change := range diff.Changes {
		for _, fieldChange := range change.Fields {
			if fieldChange.Field == field {
				set[change.Layer] = true
			}
		}
	}
	return sortedLayersSet(set)
}

func layersByType(diff Diff, types ...ChangeType) []int {
	set := map[int]bool{}
	for _, change := range diff.Changes {
		for _, kind := range types {
			if change.Type == kind {
				set[change.Layer] = true
			}
		}
	}
	return sortedLayersSet(set)
}

// targetIncreased reports layers whose target thickness moved strictly upward,
// which is what raises overcoat exposure.
func targetIncreased(diff Diff) []int {
	set := map[int]bool{}
	for _, change := range diff.Changes {
		for _, fieldChange := range change.Fields {
			if fieldChange.Field == "target_nm" && fieldChange.After > fieldChange.Before {
				set[change.Layer] = true
			}
		}
	}
	return sortedLayersSet(set)
}

// rateCrossesRotationThreshold reports layers where the rate delta moves across
// the rotation.ProfileFor boundary (rate > 1 selects 18 rpm, otherwise 12).
func rateCrossesRotationThreshold(diff Diff) []int {
	set := map[int]bool{}
	for _, change := range diff.Changes {
		for _, fieldChange := range change.Fields {
			if fieldChange.Field != "rate_nm_s" {
				continue
			}
			if (fieldChange.Before <= 1 && fieldChange.After > 1) || (fieldChange.Before > 1 && fieldChange.After <= 1) {
				set[change.Layer] = true
			}
		}
	}
	return sortedLayersSet(set)
}

// Assess derives operational impact from a frozen diff, the set of runs to
// consider (normally all registry runs), and their bindings. Runs without a
// binding are reported separately by the caller rather than guessed at.
func Assess(diff Diff, runs []*model.Run, bindings map[model.ID]RunBinding) Assessment {
	assessment := Assessment{AffectedRuns: []AffectedRun{}, QualityGates: []GateImpact{}, Interlocks: []InterlockImpact{}}
	changed := changedLayers(diff)
	allChanged := sortedLayersSet(changed)
	structural := layersByType(diff, ChangeAdded, ChangeRemoved, ChangeMoved)
	materialLayers := fieldLayers(diff, "material")
	targetLayers := fieldLayers(diff, "target_nm")
	rateLayers := fieldLayers(diff, "rate_nm_s")

	for _, r := range runs {
		binding, bound := bindings[r.ID]
		if !bound || binding.RecipeID != diff.RecipeID || binding.RevisionNumber != diff.FromNumber {
			continue
		}
		exposure := exposureFor(r.State)
		reasons := []string{
			fmt.Sprintf("bound to frozen revision %d at %s", binding.RevisionNumber, binding.Source),
		}
		layers := append([]int(nil), allChanged...)
		if exposure == ExposureInFlight && r.Layer > 0 {
			pending := []int{}
			for _, number := range layers {
				if number >= r.Layer {
					pending = append(pending, number)
				}
			}
			layers = pending
			if len(pending) == 0 {
				reasons = append(reasons, fmt.Sprintf("run passed all changed layers at layer %d; residual documentation impact only", r.Layer))
			} else {
				reasons = append(reasons, fmt.Sprintf("in flight at layer %d; changed layers at/after current position still executable", r.Layer))
			}
		}
		severityNote := ""
		switch exposure {
		case ExposureInFlight:
			severityNote = "requires disposition before the next layer start"
		case ExposureCompleted:
			severityNote = "already produced on the old revision; treat as legacy baseline"
		case ExposureFailed:
			severityNote = "failed run; any retry must use the new frozen revision"
		}
		if severityNote != "" {
			reasons = append(reasons, severityNote)
		}
		assessment.AffectedRuns = append(assessment.AffectedRuns, AffectedRun{
			RunID:         r.ID,
			Batch:         r.Batch,
			State:         r.State,
			BoundRevision: binding.RevisionNumber,
			Exposure:      exposure,
			CurrentLayer:  r.Layer,
			ChangedLayers: layers,
			Reasons:       reasons,
		})
	}
	sort.SliceStable(assessment.AffectedRuns, func(i, j int) bool { return assessment.AffectedRuns[i].RunID < assessment.AffectedRuns[j].RunID })

	addGate := func(gate GateName, layers []int, severity, reason string) {
		if len(layers) == 0 {
			return
		}
		assessment.QualityGates = append(assessment.QualityGates, GateImpact{Gate: gate, Layers: layers, Severity: severity, Reason: reason})
	}
	addGate(GateLayerEvidence, nonEmpty(materialLayers, targetLayers, rateLayers, structural), "critical", "model.EvaluateRelease layer_evidence: quartz/optical evidence was collected against the old material, target and rate and is no longer comparable")
	addGate(GateOpticalRelease, targetLayers, "critical", "run.ReleaseQuality validates the latest optical thickness sample; target thickness moved, so the optical endpoint and release decision must be re-based")
	addGate(GateIncidents, nonEmpty(materialLayers, rateLayers, structural), "warning", "model.EvaluateRelease incidents: changed deposition parameters require review of warnings/incidents raised on the old revision")
	addGate(GateRunState, assessment.inFlightLayers(allChanged), "warning", "model.EvaluateRelease run_state: in-flight runs need an explicit hold/continue disposition")

	addInterlock := func(name InterlockName, layers []int, severity, reason string) {
		if len(layers) == 0 {
			return
		}
		assessment.Interlocks = append(assessment.Interlocks, InterlockImpact{Interlock: name, Layers: layers, Severity: severity, Reason: reason})
	}
	addInterlock(InterlockOvercoat, targetIncreased(diff), "critical", "interlock.Overcoat closes the shutter past the thickness limit; an increased target requires the cutoff limit to be re-confirmed")
	addInterlock(InterlockVacuumPermit, nonEmpty(structural, materialLayers, targetLayers, rateLayers), "critical", "interlock.Permit gates coating entry on chamber vacuum and run state; any changed layer requires a fresh deposition permit")
	addInterlock(InterlockRotation, rateCrossesRotationThreshold(diff), "warning", "rotation.ProfileFor switches between 12 and 18 rpm at rate=1 nm/s; the rate delta crosses that boundary")
	addInterlock(InterlockChamberReady, rateLayers, "warning", "rate changes alter deposition thermal load; chamber ready/cooldown policy must be re-confirmed before starting")
	return assessment
}

// inFlightLayers returns allChanged when at least one affected run is in flight.
func (a Assessment) inFlightLayers(allChanged []int) []int {
	for _, affected := range a.AffectedRuns {
		if affected.Exposure == ExposureInFlight {
			return allChanged
		}
	}
	return nil
}

func nonEmpty(groups ...[]int) []int {
	set := map[int]bool{}
	for _, group := range groups {
		for _, number := range group {
			set[number] = true
		}
	}
	return sortedLayersSet(set)
}
