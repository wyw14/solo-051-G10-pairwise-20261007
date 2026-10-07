package recipe

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/wyw14/cry-161/internal/model"
	"github.com/wyw14/cry-161/internal/rotation"
)

// Change kinds classify a single difference between two frozen revisions.
const (
	ChangeLayerAdded       = "layer_added"
	ChangeLayerRemoved     = "layer_removed"
	ChangeLayerReordered   = "layer_reordered"
	ChangeMaterial         = "material"
	ChangeTargetThickness  = "target_nm"
	ChangeRate             = "rate_nm_s"
	ChangeEquipmentCommand = "equipment_command"
)

// Quality gates that can be invalidated by a recipe change. The names mirror
// the evidence collected in service.Runtime.Capture and the release decision
// in model.EvaluateRelease.
const (
	GateLayerEvidence = "layer_evidence"
	GateThickness     = "thickness_termination"
	GateRelease       = "release_decision"
)

// Safety interlocks that can be affected. The names mirror the checks in the
// interlock package and the equipment settings derived from a layer.
const (
	InterlockVacuum    = "vacuum_permit"
	InterlockOvercoat  = "overcoat_shutter_limit"
	InterlockAdmission = "source_power_admission"
	InterlockRotation  = "rotation_speed_guard"
)

// BasisFrozenRevisions is the only accepted basis for an impact report: both
// endpoints must be immutable revisions produced by RevisionStore.Freeze.
// Draft differences are never reported as released changes.
const BasisFrozenRevisions = "frozen_revisions"

// Change is one auditable difference and its precomputed blast radius.
type Change struct {
	Kind        string   `json:"kind"`
	Layer       int      `json:"layer"`
	OldPosition int      `json:"old_position,omitempty"`
	NewPosition int      `json:"new_position,omitempty"`
	Command     string   `json:"command,omitempty"`
	From        any      `json:"from,omitempty"`
	To          any      `json:"to,omitempty"`
	Detail      string   `json:"detail"`
	QualityGate []string `json:"quality_gates,omitempty"`
	Interlocks  []string `json:"interlocks,omitempty"`
}

// EquipmentCommand is one deterministic device command implied by a frozen
// revision. It is derived, never stored on the revision, so the comparison
// reflects what the plant would actually be told to do.
type EquipmentCommand struct {
	LayerNumber int               `json:"layer_number"` // 0 means run-level
	Kind        model.CommandKind `json:"kind"`
	Params      map[string]any    `json:"params,omitempty"`
}

// GateImpact names a quality gate whose pass/fail basis moves.
type GateImpact struct {
	Name        string   `json:"name"`
	Layers      []int    `json:"layers"`
	ChangeKinds []string `json:"change_kinds"`
	Reason      string   `json:"reason"`
}

// InterlockImpact names a safety interlock that must be revalidated.
type InterlockImpact struct {
	Name        string   `json:"name"`
	Layers      []int    `json:"layers"`
	ChangeKinds []string `json:"change_kinds"`
	Reason      string   `json:"reason"`
}

// AffectedRun classifies one run against the revision window.
type AffectedRun struct {
	RunID           model.ID       `json:"run_id"`
	Batch           string         `json:"batch"`
	State           model.RunState `json:"state"`
	StartedRevision int            `json:"started_revision"`
	Affected        bool           `json:"affected"`
	Reasons         []string       `json:"reasons"`
}

// RevisionRef is the verified, fingerprinted identity of one report endpoint.
type RevisionRef struct {
	ID          model.ID  `json:"id"`
	Number      int       `json:"number"`
	FrozenAt    time.Time `json:"frozen_at"`
	Layers      int       `json:"layers"`
	Fingerprint string    `json:"fingerprint"`
}

// Report is the auditable result of comparing two frozen revisions.
type Report struct {
	ID           model.ID          `json:"id"`
	RecipeID     model.ID          `json:"recipe_id"`
	Basis        string            `json:"basis"`
	FromRevision RevisionRef       `json:"from_revision"`
	ToRevision   RevisionRef       `json:"to_revision"`
	Changes      []Change          `json:"changes"`
	Runs         []AffectedRun     `json:"runs"`
	QualityGates []GateImpact      `json:"quality_gates"`
	Interlocks   []InterlockImpact `json:"interlocks"`
	// DraftsIgnored counts unpublished drafts that exist for the recipe but
	// were deliberately excluded from the comparison.
	DraftsIgnored int       `json:"drafts_ignored"`
	GeneratedAt   time.Time `json:"generated_at"`
	Fingerprint   string    `json:"fingerprint"`
}

// ErrUnfrozenRevision marks an endpoint that is not a frozen, released
// revision (for example a draft), which may not be used in an impact report.
type ErrUnfrozenRevision struct {
	RecipeID model.ID
	Number   int
}

func (e ErrUnfrozenRevision) Error() string {
	return fmt.Sprintf("recipe %s revision %d is not a frozen released revision; drafts cannot be compared as published changes", e.RecipeID, e.Number)
}

// Analyze loads both endpoints from the frozen revision store and builds the
// impact report. It fails closed: revisions that were never frozen (drafts,
// edited working copies) cannot be supplied here.
func Analyze(store *RevisionStore, recipeID model.ID, fromNumber, toNumber int, runs []*model.Run) (Report, error) {
	if store == nil {
		return Report{}, fmt.Errorf("revision store is required")
	}
	if fromNumber == toNumber {
		return Report{}, fmt.Errorf("from and to must reference different frozen revisions")
	}
	from, ok := store.Get(recipeID, fromNumber)
	if !ok {
		return Report{}, ErrUnfrozenRevision{RecipeID: recipeID, Number: fromNumber}
	}
	to, ok := store.Get(recipeID, toNumber)
	if !ok {
		return Report{}, ErrUnfrozenRevision{RecipeID: recipeID, Number: toNumber}
	}
	if fromNumber > toNumber {
		from, to = to, from
	}
	report := AnalyzeRevisions(from, to, runs)
	if err := report.Validate(); err != nil {
		return Report{}, fmt.Errorf("impact report invalid: %w", err)
	}
	return report, nil
}

// AnalyzeRevisions builds the report from two revisions already known to be
// frozen. Prefer Analyze, which enforces the frozen-only basis.
func AnalyzeRevisions(from, to Revision, runs []*model.Run) Report {
	changes := diffLayers(from.Layers, to.Layers)
	changes = append(changes, diffEquipment(from, to)...)
	sortChanges(changes)

	report := Report{
		ID:           model.NewID(),
		RecipeID:     to.RecipeID,
		Basis:        BasisFrozenRevisions,
		FromRevision: refRevision(from),
		ToRevision:   refRevision(to),
		Changes:      changes,
		Runs:         affectedRuns(to.RecipeID, from.Number, to.Number, runs),
		QualityGates: aggregateGates(changes, to.Layers),
		Interlocks:   aggregateInterlocks(changes, to.Layers),
		GeneratedAt:  time.Now().UTC(),
	}
	report.Fingerprint = reportFingerprint(report)
	return report
}

func refRevision(rev Revision) RevisionRef {
	return RevisionRef{ID: rev.ID, Number: rev.Number, FrozenAt: rev.CreatedAt, Layers: len(rev.Layers), Fingerprint: RevisionFingerprint(rev)}
}

// RevisionFingerprint hashes the immutable layer content of a frozen revision.
func RevisionFingerprint(rev Revision) string {
	type item struct {
		Number   int     `json:"number"`
		Material string  `json:"material"`
		Target   float64 `json:"target_nm"`
		Rate     float64 `json:"rate_nm_s"`
	}
	items := make([]item, 0, len(rev.Layers))
	for _, layer := range rev.Layers {
		items = append(items, item{layer.Number, layer.Material, layer.TargetNanometers, layer.RateNanometers})
	}
	return hashJSON(struct {
		RecipeID model.ID `json:"recipe_id"`
		Number   int      `json:"number"`
		Layers   []item   `json:"layers"`
	}{rev.RecipeID, rev.Number, items})
}

func reportFingerprint(report Report) string {
	return hashJSON(struct {
		RecipeID model.ID `json:"recipe_id"`
		From     model.ID `json:"from_id"`
		To       model.ID `json:"to_id"`
		Changes  []Change `json:"changes"`
	}{report.RecipeID, report.FromRevision.ID, report.ToRevision.ID, report.Changes})
}

func hashJSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	sum := fnv.New128a()
	_, _ = sum.Write(data)
	return fmt.Sprintf("%x", sum.Sum(nil))
}

// Validate enforces the audit invariants: frozen basis, resolved endpoints,
// and changes that only reference positions present in the cited revisions.
func (r Report) Validate() error {
	if r.ID == "" || r.RecipeID == "" {
		return fmt.Errorf("report identity is required")
	}
	if r.Basis != BasisFrozenRevisions {
		return fmt.Errorf("report basis %q is not %s", r.Basis, BasisFrozenRevisions)
	}
	if r.FromRevision.ID == "" || r.ToRevision.ID == "" {
		return fmt.Errorf("both revisions must be frozen revisions with identity")
	}
	if r.FromRevision.ID == r.ToRevision.ID {
		return fmt.Errorf("from and to revisions are identical")
	}
	if r.FromRevision.Fingerprint == "" || r.ToRevision.Fingerprint == "" || r.Fingerprint == "" {
		return fmt.Errorf("report fingerprints are missing")
	}
	if r.GeneratedAt.IsZero() {
		return fmt.Errorf("report generation time is missing")
	}
	knownGates := map[string]bool{GateLayerEvidence: true, GateThickness: true, GateRelease: true}
	knownInterlocks := map[string]bool{InterlockVacuum: true, InterlockOvercoat: true, InterlockAdmission: true, InterlockRotation: true}
	for _, change := range r.Changes {
		if change.Layer < 0 {
			return fmt.Errorf("change %s has no layer anchor", change.Kind)
		}
		switch change.Kind {
		case ChangeLayerAdded:
			if change.NewPosition < 1 || change.NewPosition > r.ToRevision.Layers {
				return fmt.Errorf("added layer position outside to revision")
			}
		case ChangeLayerRemoved:
			if change.OldPosition < 1 || change.OldPosition > r.FromRevision.Layers {
				return fmt.Errorf("removed layer position outside from revision")
			}
		case ChangeMaterial, ChangeTargetThickness, ChangeRate, ChangeLayerReordered, ChangeEquipmentCommand:
			if change.Layer > 0 && (change.NewPosition > r.ToRevision.Layers || change.OldPosition > r.FromRevision.Layers) {
				return fmt.Errorf("change %s references layer outside the compared revisions", change.Kind)
			}
		default:
			return fmt.Errorf("unknown change kind %q", change.Kind)
		}
		for _, gate := range change.QualityGate {
			if !knownGates[gate] {
				return fmt.Errorf("change references unknown quality gate %q", gate)
			}
		}
		for _, interlock := range change.Interlocks {
			if !knownInterlocks[interlock] {
				return fmt.Errorf("change references unknown interlock %q", interlock)
			}
		}
	}
	return nil
}

// diffLayers aligns the two ordered layer stacks by material identity using a
// longest-common-subsequence, then records additions, removals, reordering and
// per-attribute changes for matched layers.
func diffLayers(oldLayers, newLayers []model.Layer) []Change {
	changes := make([]Change, 0)
	n, m := len(oldLayers), len(newLayers)
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := 1; i <= n; i++ {
		for j := 1; j <= m; j++ {
			if oldLayers[i-1].Material == newLayers[j-1].Material {
				lcs[i][j] = lcs[i-1][j-1] + 1
			} else if lcs[i-1][j] >= lcs[i][j-1] {
				lcs[i][j] = lcs[i-1][j]
			} else {
				lcs[i][j] = lcs[i][j-1]
			}
		}
	}
	oldMatched := make([]bool, n)
	newMatched := make([]bool, m)
	type pair struct{ oldIndex, newIndex int }
	var pairs []pair
	i, j := n, m
	for i > 0 && j > 0 {
		if oldLayers[i-1].Material == newLayers[j-1].Material {
			pairs = append(pairs, pair{i - 1, j - 1})
			oldMatched[i-1] = true
			newMatched[j-1] = true
			i--
			j--
		} else if lcs[i-1][j] >= lcs[i][j-1] {
			i--
		} else {
			j--
		}
	}
	for left, right := 0, len(pairs)-1; left < right; left, right = left+1, right-1 {
		pairs[left], pairs[right] = pairs[right], pairs[left]
	}
	// Unmatched layers become an in-place material change only when both
	// versions leave a different material at the SAME position. Any other
	// imbalance is a genuine insertion or removal; shifts of identity-matched
	// layers are already recorded above as reorders.
	unmatchedOldByPos := map[int]int{}
	unmatchedNewByPos := map[int]int{}
	for index := range oldLayers {
		if !oldMatched[index] {
			unmatchedOldByPos[index+1] = index
		}
	}
	for index := range newLayers {
		if !newMatched[index] {
			unmatchedNewByPos[index+1] = index
		}
	}
	swapped := map[int]bool{}
	for position, oldIndex := range unmatchedOldByPos {
		newIndex, samePosition := unmatchedNewByPos[position]
		if !samePosition {
			continue
		}
		swapped[position] = true
		oldLayer, newLayer := oldLayers[oldIndex], newLayers[newIndex]
		changes = append(changes, Change{
			Kind:        ChangeMaterial,
			Layer:       newLayer.Number,
			OldPosition: position,
			NewPosition: position,
			From:        oldLayer.Material,
			To:          newLayer.Material,
			Detail:      fmt.Sprintf("position %d material %s -> %s", position, oldLayer.Material, newLayer.Material),
			QualityGate: gatesFor(ChangeMaterial),
			Interlocks:  interlocksFor(ChangeMaterial),
		})
		if oldLayer.TargetNanometers != newLayer.TargetNanometers {
			changes = append(changes, Change{
				Kind:        ChangeTargetThickness,
				Layer:       newLayer.Number,
				OldPosition: position,
				NewPosition: position,
				From:        oldLayer.TargetNanometers,
				To:          newLayer.TargetNanometers,
				Detail:      fmt.Sprintf("%s target thickness %s -> %s nm", newLayer.Material, formatFloat(oldLayer.TargetNanometers), formatFloat(newLayer.TargetNanometers)),
				QualityGate: gatesFor(ChangeTargetThickness),
				Interlocks:  interlocksFor(ChangeTargetThickness),
			})
		}
		if oldLayer.RateNanometers != newLayer.RateNanometers {
			changes = append(changes, Change{
				Kind:        ChangeRate,
				Layer:       newLayer.Number,
				OldPosition: position,
				NewPosition: position,
				From:        oldLayer.RateNanometers,
				To:          newLayer.RateNanometers,
				Detail:      fmt.Sprintf("%s deposition rate %s -> %s nm/s", newLayer.Material, formatFloat(oldLayer.RateNanometers), formatFloat(newLayer.RateNanometers)),
				QualityGate: gatesFor(ChangeRate),
				Interlocks:  interlocksFor(ChangeRate),
			})
		}
	}
	for position, newIndex := range unmatchedNewByPos {
		if swapped[position] {
			continue
		}
		layer := newLayers[newIndex]
		changes = append(changes, Change{
			Kind:        ChangeLayerAdded,
			Layer:       layer.Number,
			NewPosition: position,
			From:        nil,
			To:          layer.Material,
			Detail:      fmt.Sprintf("position %d adds %s (target %s nm at %s nm/s)", position, layer.Material, formatFloat(layer.TargetNanometers), formatFloat(layer.RateNanometers)),
			QualityGate: gatesFor(ChangeLayerAdded),
			Interlocks:  interlocksFor(ChangeLayerAdded),
		})
	}
	for position, oldIndex := range unmatchedOldByPos {
		if swapped[position] {
			continue
		}
		layer := oldLayers[oldIndex]
		changes = append(changes, Change{
			Kind:        ChangeLayerRemoved,
			Layer:       layer.Number,
			OldPosition: position,
			From:        layer.Material,
			Detail:      fmt.Sprintf("position %d removes %s (target %s nm at %s nm/s)", position, layer.Material, formatFloat(layer.TargetNanometers), formatFloat(layer.RateNanometers)),
			QualityGate: gatesFor(ChangeLayerRemoved),
			Interlocks:  interlocksFor(ChangeLayerRemoved),
		})
	}
	for _, matched := range pairs {
		oldLayer := oldLayers[matched.oldIndex]
		newLayer := newLayers[matched.newIndex]
		oldPos, newPos := matched.oldIndex+1, matched.newIndex+1
		if oldPos != newPos {
			changes = append(changes, Change{
				Kind:        ChangeLayerReordered,
				Layer:       newLayer.Number,
				OldPosition: oldPos,
				NewPosition: newPos,
				From:        oldLayer.Material,
				To:          newLayer.Material,
				Detail:      fmt.Sprintf("%s moves from position %d to %d", newLayer.Material, oldPos, newPos),
				QualityGate: gatesFor(ChangeLayerReordered),
				Interlocks:  interlocksFor(ChangeLayerReordered),
			})
		}
		if oldLayer.Material != newLayer.Material {
			changes = append(changes, Change{
				Kind:        ChangeMaterial,
				Layer:       newLayer.Number,
				OldPosition: oldPos,
				NewPosition: newPos,
				From:        oldLayer.Material,
				To:          newLayer.Material,
				Detail:      fmt.Sprintf("position %d material %s -> %s", newPos, oldLayer.Material, newLayer.Material),
				QualityGate: gatesFor(ChangeMaterial),
				Interlocks:  interlocksFor(ChangeMaterial),
			})
		}
		if oldLayer.TargetNanometers != newLayer.TargetNanometers {
			changes = append(changes, Change{
				Kind:        ChangeTargetThickness,
				Layer:       newLayer.Number,
				OldPosition: oldPos,
				NewPosition: newPos,
				From:        oldLayer.TargetNanometers,
				To:          newLayer.TargetNanometers,
				Detail:      fmt.Sprintf("%s target thickness %s -> %s nm", newLayer.Material, formatFloat(oldLayer.TargetNanometers), formatFloat(newLayer.TargetNanometers)),
				QualityGate: gatesFor(ChangeTargetThickness),
				Interlocks:  interlocksFor(ChangeTargetThickness),
			})
		}
		if oldLayer.RateNanometers != newLayer.RateNanometers {
			changes = append(changes, Change{
				Kind:        ChangeRate,
				Layer:       newLayer.Number,
				OldPosition: oldPos,
				NewPosition: newPos,
				From:        oldLayer.RateNanometers,
				To:          newLayer.RateNanometers,
				Detail:      fmt.Sprintf("%s deposition rate %s -> %s nm/s", newLayer.Material, formatFloat(oldLayer.RateNanometers), formatFloat(newLayer.RateNanometers)),
				QualityGate: gatesFor(ChangeRate),
				Interlocks:  interlocksFor(ChangeRate),
			})
		}
	}
	return changes
}

// EquipmentCommands derives the device command sequence for one revision:
// run-level pump/vent plus per-layer rotation, source conditioning, shutter
// open and shutter close. Numeric parameters reuse the production functions
// (rotation.ProfileFor, source admission weight = rate, overcoat limit =
// target thickness) so a parameter change here predicts a real command change.
func EquipmentCommands(rev Revision) []EquipmentCommand {
	commands := []EquipmentCommand{{LayerNumber: 0, Kind: model.CommandPump}}
	for _, layer := range rev.Layers {
		profile := rotation.ProfileFor(layer)
		commands = append(commands,
			EquipmentCommand{LayerNumber: layer.Number, Kind: model.CommandRotate, Params: map[string]any{"rpm": profile.RPM, "duration_s": profile.DurationSeconds, "rate_nm_s": layer.RateNanometers}},
			EquipmentCommand{LayerNumber: layer.Number, Kind: model.CommandCondition, Params: map[string]any{"power_weight": layer.RateNanometers, "material": layer.Material}},
			EquipmentCommand{LayerNumber: layer.Number, Kind: model.CommandOpenShutter, Params: map[string]any{"material": layer.Material}},
			EquipmentCommand{LayerNumber: layer.Number, Kind: model.CommandCloseShutter, Params: map[string]any{"overcoat_limit_nm": layer.TargetNanometers, "material": layer.Material}},
		)
	}
	commands = append(commands, EquipmentCommand{LayerNumber: 0, Kind: model.CommandVent})
	return commands
}

type commandKey struct {
	layer int
	kind  model.CommandKind
}

func diffEquipment(from, to Revision) []Change {
	oldCommands := map[commandKey]EquipmentCommand{}
	newCommands := map[commandKey]EquipmentCommand{}
	oldOrder, newOrder := map[commandKey]int{}, map[commandKey]int{}
	for index, command := range EquipmentCommands(from) {
		key := commandKey{command.LayerNumber, command.Kind}
		oldCommands[key] = command
		oldOrder[key] = index
	}
	for index, command := range EquipmentCommands(to) {
		key := commandKey{command.LayerNumber, command.Kind}
		newCommands[key] = command
		newOrder[key] = index
	}
	keys := make(map[commandKey]bool)
	for key := range oldCommands {
		keys[key] = true
	}
	for key := range newCommands {
		keys[key] = true
	}
	changes := make([]Change, 0)
	for key := range keys {
		oldCommand, inOld := oldCommands[key]
		newCommand, inNew := newCommands[key]
		switch {
		case inOld && inNew && canonicalParams(oldCommand.Params) == canonicalParams(newCommand.Params):
			continue
		case inOld && !inNew:
			changes = append(changes, Change{
				Kind:        ChangeEquipmentCommand,
				Layer:       key.layer,
				OldPosition: key.layer,
				Command:     string(key.kind),
				From:        oldCommand.Params,
				Detail:      fmt.Sprintf("layer %d loses command %s", key.layer, key.kind),
				QualityGate: gatesForCommand(key.kind),
				Interlocks:  []string{interlockForCommand(key.kind)},
			})
		case !inOld && inNew:
			changes = append(changes, Change{
				Kind:        ChangeEquipmentCommand,
				Layer:       key.layer,
				NewPosition: key.layer,
				Command:     string(key.kind),
				To:          newCommand.Params,
				Detail:      fmt.Sprintf("layer %d gains command %s %s", key.layer, key.kind, canonicalParams(newCommand.Params)),
				QualityGate: gatesForCommand(key.kind),
				Interlocks:  []string{interlockForCommand(key.kind)},
			})
		default:
			changes = append(changes, Change{
				Kind:        ChangeEquipmentCommand,
				Layer:       key.layer,
				OldPosition: key.layer,
				NewPosition: key.layer,
				Command:     string(key.kind),
				From:        oldCommand.Params,
				To:          newCommand.Params,
				Detail:      fmt.Sprintf("layer %d command %s changes %s -> %s", key.layer, key.kind, canonicalParams(oldCommand.Params), canonicalParams(newCommand.Params)),
				QualityGate: gatesForCommand(key.kind),
				Interlocks:  []string{interlockForCommand(key.kind)},
			})
		}
	}
	return changes
}

func canonicalParams(params map[string]any) string {
	if len(params) == 0 {
		return "{}"
	}
	keys := make([]string, 0, len(params))
	for key := range params {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", key, params[key]))
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func interlockForCommand(kind model.CommandKind) string {
	switch kind {
	case model.CommandPump:
		return InterlockVacuum
	case model.CommandRotate:
		return InterlockRotation
	case model.CommandCondition:
		return InterlockAdmission
	case model.CommandOpenShutter, model.CommandCloseShutter:
		return InterlockOvercoat
	default:
		return InterlockOvercoat
	}
}

func gatesForCommand(kind model.CommandKind) []string {
	switch kind {
	case model.CommandCloseShutter:
		return []string{GateThickness, GateLayerEvidence}
	default:
		return []string{GateLayerEvidence}
	}
}

func gatesFor(kind string) []string {
	switch kind {
	case ChangeTargetThickness:
		return []string{GateThickness, GateLayerEvidence, GateRelease}
	case ChangeMaterial:
		return []string{GateLayerEvidence, GateRelease}
	case ChangeRate:
		return []string{GateLayerEvidence, GateRelease}
	default:
		return []string{GateLayerEvidence, GateRelease}
	}
}

func interlocksFor(kind string) []string {
	switch kind {
	case ChangeTargetThickness:
		return []string{InterlockOvercoat}
	case ChangeRate:
		return []string{InterlockAdmission, InterlockRotation}
	case ChangeMaterial:
		return nil
	case ChangeLayerReordered:
		return []string{InterlockOvercoat, InterlockAdmission, InterlockRotation}
	default:
		return []string{InterlockOvercoat, InterlockAdmission, InterlockRotation}
	}
}

type changeIndex struct {
	kinds  map[string]bool
	layers map[int]bool
}

func newChangeIndex() changeIndex {
	return changeIndex{kinds: map[string]bool{}, layers: map[int]bool{}}
}

func (c changeIndex) add(kind string, layer int) {
	c.kinds[kind] = true
	if layer > 0 {
		c.layers[layer] = true
	}
}

func (c changeIndex) sortedLayers() []int {
	layers := make([]int, 0, len(c.layers))
	for layer := range c.layers {
		layers = append(layers, layer)
	}
	sort.Ints(layers)
	return layers
}

func (c changeIndex) sortedKinds() []string {
	kinds := make([]string, 0, len(c.kinds))
	for kind := range c.kinds {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	return kinds
}

func aggregateGates(changes []Change, newLayers []model.Layer) []GateImpact {
	indexes := map[string]changeIndex{}
	for _, change := range changes {
		position := change.NewPosition
		if position == 0 {
			position = change.Layer
		}
		for _, gate := range change.QualityGate {
			index := indexes[gate]
			if index.kinds == nil {
				index = newChangeIndex()
			}
			index.add(change.Kind, position)
			indexes[gate] = index
		}
	}
	_ = newLayers
	order := []string{GateThickness, GateLayerEvidence, GateRelease}
	reasons := map[string]string{
		GateThickness:     "optical termination limit derives from target thickness; termination evidence must be revalidated",
		GateLayerEvidence: "quartz/optical layer evidence is captured per layer; the evidence baseline changes for the listed layers",
		GateRelease:       "the release decision aggregates layer evidence (model.EvaluateRelease); the acceptance basis changes",
	}
	result := make([]GateImpact, 0, len(order))
	for _, gate := range order {
		index, ok := indexes[gate]
		if !ok {
			continue
		}
		result = append(result, GateImpact{Name: gate, Layers: index.sortedLayers(), ChangeKinds: index.sortedKinds(), Reason: reasons[gate]})
	}
	return result
}

func aggregateInterlocks(changes []Change, newLayers []model.Layer) []InterlockImpact {
	indexes := map[string]changeIndex{}
	for _, change := range changes {
		position := change.NewPosition
		if position == 0 {
			position = change.OldPosition
		}
		for _, interlock := range change.Interlocks {
			index := indexes[interlock]
			if index.kinds == nil {
				index = newChangeIndex()
			}
			index.add(change.Kind, position)
			indexes[interlock] = index
		}
	}
	order := []string{InterlockVacuum, InterlockOvercoat, InterlockAdmission, InterlockRotation}
	reasons := map[string]string{
		InterlockVacuum:    "vacuum permit gates run state (interlock.Permit); revalidate chamber-state entry conditions",
		InterlockOvercoat:  "shutter force-close threshold derives from target thickness (interlock.Overcoat); new overcoat limits must be revalidated",
		InterlockAdmission: "source admission power weight derives from deposition rate (source.Controller.Layer); the power budget changes",
		InterlockRotation:  "rotation RPM and duration derive from deposition rate and thickness (rotation.ProfileFor); the speed envelope changes",
	}
	result := make([]InterlockImpact, 0, len(order))
	for _, interlock := range order {
		index, ok := indexes[interlock]
		if !ok {
			continue
		}
		result = append(result, InterlockImpact{Name: interlock, Layers: index.sortedLayers(), ChangeKinds: index.sortedKinds(), Reason: reasons[interlock]})
	}
	return result
}

func activeStates() map[model.RunState]bool {
	return map[model.RunState]bool{
		model.Loading:      true,
		model.Pumping:      true,
		model.Conditioning: true,
		model.Coating:      true,
		model.Cooling:      true,
		model.Venting:      true,
	}
}

func affectedRuns(recipeID model.ID, fromNumber, toNumber int, runs []*model.Run) []AffectedRun {
	active := activeStates()
	result := make([]AffectedRun, 0)
	for _, current := range runs {
		if current == nil || current.RecipeID != recipeID {
			continue
		}
		entry := AffectedRun{RunID: current.ID, Batch: current.Batch, State: current.State, StartedRevision: current.RecipeRevision}
		isActive := active[current.State]
		switch {
		case current.RecipeRevision == 0:
			if isActive {
				entry.Affected = true
				entry.Reasons = []string{"started revision unrecorded; fail-safe classification as affected"}
			} else {
				entry.Reasons = []string{"completed run with unrecorded revision; historical evidence retained"}
			}
		case current.RecipeRevision >= toNumber:
			entry.Reasons = []string{fmt.Sprintf("already executing revision %d", current.RecipeRevision)}
		case current.RecipeRevision == fromNumber || (current.RecipeRevision > fromNumber && current.RecipeRevision < toNumber) || current.RecipeRevision < fromNumber:
			if isActive {
				entry.Affected = true
				entry.Reasons = []string{fmt.Sprintf("active run on revision %d while released revision is %d", current.RecipeRevision, toNumber)}
			} else {
				entry.Reasons = []string{fmt.Sprintf("completed under revision %d; evidence baseline unchanged", current.RecipeRevision)}
			}
		}
		result = append(result, entry)
	}
	sort.Slice(result, func(a, b int) bool {
		if result[a].Affected != result[b].Affected {
			return result[a].Affected
		}
		return result[a].RunID < result[b].RunID
	})
	return result
}

func sortChanges(changes []Change) {
	sort.SliceStable(changes, func(a, b int) bool {
		left := changes[a].NewPosition
		if left == 0 {
			left = changes[a].OldPosition
		}
		right := changes[b].NewPosition
		if right == 0 {
			right = changes[b].OldPosition
		}
		if left != right {
			return left < right
		}
		return changes[a].Kind < changes[b].Kind
	})
}

func formatFloat(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

// RenderText produces the deterministic, human-auditable rendering of a report.
func (r Report) RenderText() string {
	var builder strings.Builder
	builder.WriteString("OPTICCOAT RECIPE IMPACT ANALYSIS - AUDIT REPORT\n")
	builder.WriteString(fmt.Sprintf("report_id: %s\n", r.ID))
	builder.WriteString(fmt.Sprintf("recipe_id: %s\n", r.RecipeID))
	builder.WriteString(fmt.Sprintf("basis: %s (draft differences are not published changes)\n", r.Basis))
	writeRevisionRef(&builder, "from", r.FromRevision)
	writeRevisionRef(&builder, "to  ", r.ToRevision)
	builder.WriteString(fmt.Sprintf("generated_at: %s\n", r.GeneratedAt.Format(time.RFC3339)))
	if r.DraftsIgnored > 0 {
		builder.WriteString(fmt.Sprintf("drafts_ignored: %d unpublished draft(s) excluded\n", r.DraftsIgnored))
	}
	builder.WriteString(fmt.Sprintf("\nCHANGES (%d):\n", len(r.Changes)))
	for _, change := range r.Changes {
		builder.WriteString(fmt.Sprintf(" - [%s] %s\n", change.Kind, change.Detail))
		if len(change.QualityGate) > 0 || len(change.Interlocks) > 0 {
			builder.WriteString(fmt.Sprintf("     gates=%s interlocks=%s\n", strings.Join(change.QualityGate, ","), strings.Join(change.Interlocks, ",")))
		}
	}
	affected := 0
	for _, entry := range r.Runs {
		if entry.Affected {
			affected++
		}
	}
	builder.WriteString(fmt.Sprintf("\nAFFECTED RUNS (%d affected of %d evaluated):\n", affected, len(r.Runs)))
	for _, entry := range r.Runs {
		flag := "no"
		if entry.Affected {
			flag = "YES"
		}
		builder.WriteString(fmt.Sprintf(" - run=%s batch=%q state=%s started_rev=%d affected=%s reasons=%s\n", entry.RunID, entry.Batch, entry.State, entry.StartedRevision, flag, strings.Join(entry.Reasons, "; ")))
	}
	builder.WriteString(fmt.Sprintf("\nQUALITY GATES (%d):\n", len(r.QualityGates)))
	for _, gate := range r.QualityGates {
		builder.WriteString(fmt.Sprintf(" - %s layers=%v changes=%s\n   %s\n", gate.Name, gate.Layers, strings.Join(gate.ChangeKinds, ","), gate.Reason))
	}
	builder.WriteString(fmt.Sprintf("\nSAFETY INTERLOCKS (%d):\n", len(r.Interlocks)))
	for _, interlock := range r.Interlocks {
		builder.WriteString(fmt.Sprintf(" - %s layers=%v changes=%s\n   %s\n", interlock.Name, interlock.Layers, strings.Join(interlock.ChangeKinds, ","), interlock.Reason))
	}
	builder.WriteString(fmt.Sprintf("\nfingerprint: %s\n", r.Fingerprint))
	return builder.String()
}

func writeRevisionRef(builder *strings.Builder, label string, ref RevisionRef) {
	builder.WriteString(fmt.Sprintf("%s revision=%d frozen_at=%s id=%s layers=%d fingerprint=%s\n", label, ref.Number, ref.FrozenAt.Format(time.RFC3339), ref.ID, ref.Layers, ref.Fingerprint))
}
