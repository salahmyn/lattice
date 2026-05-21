package patch

import (
	"fmt"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// decode converts a patch-argument value (a YAML/JSON-decoded map) into a
// typed schema struct by round-tripping through YAML, which honors the
// schema's yaml tags.
func decode(v interface{}, target interface{}) error {
	b, err := yaml.Marshal(v)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(b, target)
}

func argString(op schema.Operation, key string) (string, error) {
	v, ok := op.Args[key]
	if !ok {
		return "", fmt.Errorf("%s: missing argument %q", op.Op, key)
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("%s: argument %q must be a string", op.Op, key)
	}
	return s, nil
}

func argInt(op schema.Operation, key string) (int, error) {
	v, ok := op.Args[key]
	if !ok {
		return 0, fmt.Errorf("%s: missing argument %q", op.Op, key)
	}
	switch n := v.(type) {
	case int:
		return n, nil
	case int64:
		return int(n), nil
	case float64:
		return int(n), nil
	default:
		return 0, fmt.Errorf("%s: argument %q must be a number", op.Op, key)
	}
}

// applyToManifest applies a sequence of operations to a manifest in place.
func applyToManifest(m *schema.Manifest, ops []schema.Operation) error {
	for _, op := range ops {
		if err := applyManifestOp(m, op); err != nil {
			return err
		}
	}
	return nil
}

func applyManifestOp(m *schema.Manifest, op schema.Operation) error {
	switch op.Op {
	case schema.OpAddCapability:
		var c schema.Capability
		if err := decode(op.Args["capability"], &c); err != nil {
			return fmt.Errorf("%s: %w", op.Op, err)
		}
		m.Capabilities = append(m.Capabilities, c)
	case schema.OpModifyCapability:
		id, err := argString(op, "id")
		if err != nil {
			return err
		}
		var c schema.Capability
		if err := decode(op.Args["capability"], &c); err != nil {
			return fmt.Errorf("%s: %w", op.Op, err)
		}
		if !replaceCapability(m, id, c) {
			return fmt.Errorf("%s: capability %q not found", op.Op, id)
		}
	case schema.OpRemoveCapability:
		id, err := argString(op, "id")
		if err != nil {
			return err
		}
		m.Capabilities = filterCapabilities(m.Capabilities, id)
	case schema.OpAddInvariant:
		var inv schema.Invariant
		if err := decode(op.Args["invariant"], &inv); err != nil {
			return fmt.Errorf("%s: %w", op.Op, err)
		}
		m.Invariants = append(m.Invariants, inv)
	case schema.OpModifyInvariant:
		id, err := argString(op, "id")
		if err != nil {
			return err
		}
		var inv schema.Invariant
		if err := decode(op.Args["invariant"], &inv); err != nil {
			return fmt.Errorf("%s: %w", op.Op, err)
		}
		found := false
		for i := range m.Invariants {
			if m.Invariants[i].ID == id {
				m.Invariants[i] = inv
				found = true
			}
		}
		if !found {
			return fmt.Errorf("%s: invariant %q not found", op.Op, id)
		}
	case schema.OpRemoveInvariant:
		id, err := argString(op, "id")
		if err != nil {
			return err
		}
		var keep []schema.Invariant
		for _, inv := range m.Invariants {
			if inv.ID != id {
				keep = append(keep, inv)
			}
		}
		m.Invariants = keep
	case schema.OpAddDependency:
		f, err := argString(op, "feature")
		if err != nil {
			return err
		}
		if !contains(m.DependsOn, f) {
			m.DependsOn = append(m.DependsOn, f)
		}
	case schema.OpRemoveDependency:
		f, err := argString(op, "feature")
		if err != nil {
			return err
		}
		m.DependsOn = remove(m.DependsOn, f)
	case schema.OpAddSurface:
		var s schema.Surface
		if err := decode(op.Args["surface"], &s); err != nil {
			return fmt.Errorf("%s: %w", op.Op, err)
		}
		m.Surface = append(m.Surface, s)
	case schema.OpModifySurface:
		idx, err := argInt(op, "index")
		if err != nil {
			return err
		}
		if idx < 0 || idx >= len(m.Surface) {
			return fmt.Errorf("%s: surface index %d out of range", op.Op, idx)
		}
		var s schema.Surface
		if err := decode(op.Args["surface"], &s); err != nil {
			return fmt.Errorf("%s: %w", op.Op, err)
		}
		m.Surface[idx] = s
	case schema.OpRemoveSurface:
		idx, err := argInt(op, "index")
		if err != nil {
			return err
		}
		if idx < 0 || idx >= len(m.Surface) {
			return fmt.Errorf("%s: surface index %d out of range", op.Op, idx)
		}
		m.Surface = append(m.Surface[:idx], m.Surface[idx+1:]...)
	case schema.OpAddDecision:
		var d schema.Decision
		if err := decode(op.Args["decision"], &d); err != nil {
			return fmt.Errorf("%s: %w", op.Op, err)
		}
		m.Decisions = append(m.Decisions, d)
	case schema.OpAddRole:
		var r schema.Role
		if err := decode(op.Args["role"], &r); err != nil {
			return fmt.Errorf("%s: %w", op.Op, err)
		}
		m.Roles = append(m.Roles, r)
	case schema.OpModifyRole:
		id, err := argString(op, "id")
		if err != nil {
			return err
		}
		var r schema.Role
		if err := decode(op.Args["role"], &r); err != nil {
			return fmt.Errorf("%s: %w", op.Op, err)
		}
		found := false
		for i := range m.Roles {
			if m.Roles[i].ID == id {
				m.Roles[i] = r
				found = true
			}
		}
		if !found {
			return fmt.Errorf("%s: role %q not found", op.Op, id)
		}
	case schema.OpRemoveRole:
		id, err := argString(op, "id")
		if err != nil {
			return err
		}
		var keep []schema.Role
		for _, r := range m.Roles {
			if r.ID != id {
				keep = append(keep, r)
			}
		}
		m.Roles = keep
	case schema.OpAddStructuralCheck:
		var sc schema.StructuralCheck
		if err := decode(op.Args["check"], &sc); err != nil {
			return fmt.Errorf("%s: %w", op.Op, err)
		}
		m.StructuralChecks = append(m.StructuralChecks, sc)
	case schema.OpRemoveStructuralCheck:
		id, err := argString(op, "id")
		if err != nil {
			return err
		}
		var keep []schema.StructuralCheck
		for _, sc := range m.StructuralChecks {
			if sc.ID != id {
				keep = append(keep, sc)
			}
		}
		m.StructuralChecks = keep
	case schema.OpSetStatus:
		s, err := argString(op, "status")
		if err != nil {
			return err
		}
		m.Status = schema.Status(s)
	case schema.OpSetMigration:
		var mig schema.Migration
		if err := decode(op.Args["migration"], &mig); err != nil {
			return fmt.Errorf("%s: %w", op.Op, err)
		}
		m.Migration = &mig
	case schema.OpSetField:
		return applySetField(m, op)
	default:
		return fmt.Errorf("unsupported manifest operation %q", op.Op)
	}
	return nil
}

// applyToInitiative applies operations to an initiative in place.
func applyToInitiative(in *schema.Initiative, ops []schema.Operation) error {
	for _, op := range ops {
		switch op.Op {
		case schema.OpAddStream:
			var s schema.Stream
			if err := decode(op.Args["stream"], &s); err != nil {
				return fmt.Errorf("%s: %w", op.Op, err)
			}
			in.Streams = append(in.Streams, s)
		case schema.OpRemoveStream:
			id, err := argString(op, "id")
			if err != nil {
				return err
			}
			var keep []schema.Stream
			for _, s := range in.Streams {
				if s.ID != id {
					keep = append(keep, s)
				}
			}
			in.Streams = keep
		case schema.OpAddSuccessCriterion:
			var sc schema.SuccessCriterion
			if err := decode(op.Args["criterion"], &sc); err != nil {
				return fmt.Errorf("%s: %w", op.Op, err)
			}
			in.SuccessCriteria = append(in.SuccessCriteria, sc)
		case schema.OpModifySuccessCriterion:
			id, err := argString(op, "id")
			if err != nil {
				return err
			}
			var sc schema.SuccessCriterion
			if err := decode(op.Args["criterion"], &sc); err != nil {
				return fmt.Errorf("%s: %w", op.Op, err)
			}
			found := false
			for i := range in.SuccessCriteria {
				if in.SuccessCriteria[i].ID == id {
					in.SuccessCriteria[i] = sc
					found = true
				}
			}
			if !found {
				return fmt.Errorf("%s: success criterion %q not found", op.Op, id)
			}
		case schema.OpAddContract:
			var ct schema.Contract
			if err := decode(op.Args["contract"], &ct); err != nil {
				return fmt.Errorf("%s: %w", op.Op, err)
			}
			in.Contracts = append(in.Contracts, ct)
		case schema.OpLockContract:
			path, err := argString(op, "path")
			if err != nil {
				return err
			}
			found := false
			for i := range in.Contracts {
				if in.Contracts[i].Path == path {
					in.Contracts[i].LockedAt = time.Now().UTC().Format(time.RFC3339)
					found = true
				}
			}
			if !found {
				return fmt.Errorf("%s: contract %q not found", op.Op, path)
			}
		case schema.OpSetInitiativeStatus:
			s, err := argString(op, "status")
			if err != nil {
				return err
			}
			in.Status = schema.InitiativeStatus(s)
		case schema.OpSetField:
			if err := applySetField(in, op); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported initiative operation %q", op.Op)
		}
	}
	return nil
}

// applyToTask applies operations to a task in place.
func applyToTask(t *schema.Task, ops []schema.Operation) error {
	for _, op := range ops {
		switch op.Op {
		case schema.OpModifyTask:
			var nt schema.Task
			if err := decode(op.Args["task"], &nt); err != nil {
				return fmt.Errorf("%s: %w", op.Op, err)
			}
			nt.SourcePath = t.SourcePath
			*t = nt
		case schema.OpAddTaskDependency:
			var dep schema.TaskDep
			if err := decode(op.Args["dependency"], &dep); err != nil {
				return fmt.Errorf("%s: %w", op.Op, err)
			}
			t.DependsOn = append(t.DependsOn, dep)
		case schema.OpRemoveTaskDependency:
			var keep []schema.TaskDep
			tgtTask, _ := op.Args["task"].(string)
			tgtContract, _ := op.Args["contract"].(string)
			for _, d := range t.DependsOn {
				if (tgtTask != "" && d.Task == tgtTask) || (tgtContract != "" && d.Contract == tgtContract) {
					continue
				}
				keep = append(keep, d)
			}
			t.DependsOn = keep
		case schema.OpSetTaskOwner:
			owner, err := argString(op, "owner")
			if err != nil {
				return err
			}
			t.Owner = owner
		case schema.OpSetTaskStatus:
			s, err := argString(op, "status")
			if err != nil {
				return err
			}
			t.Status = schema.TaskStatus(s)
			t.StatusSource = schema.StatusManual
		case schema.OpSetField:
			if err := applySetField(t, op); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported task operation %q", op.Op)
		}
	}
	return nil
}

// --- shared small helpers ---

func replaceCapability(m *schema.Manifest, id string, c schema.Capability) bool {
	for i := range m.Capabilities {
		if m.Capabilities[i].ID == id {
			m.Capabilities[i] = c
			return true
		}
	}
	return false
}

func filterCapabilities(caps []schema.Capability, removeID string) []schema.Capability {
	var keep []schema.Capability
	for _, c := range caps {
		if c.ID != removeID {
			keep = append(keep, c)
		}
	}
	return keep
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func remove(s []string, v string) []string {
	var out []string
	for _, x := range s {
		if x != v {
			out = append(out, x)
		}
	}
	return out
}
