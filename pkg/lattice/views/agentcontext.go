package views

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// AgentContext is the pre-assembled bundle returned by `lattice agent context`.
// It is sufficient for an external agent to act on a task without
// re-analyzing the repository (design section 31).
type AgentContext struct {
	Task                  *TaskContext      `json:"task,omitempty"`
	Manifests             []ManifestContext `json:"manifests"`
	Contracts             []ContractContext `json:"contracts,omitempty"`
	CurrentCode           []CodeContext     `json:"current_code,omitempty"`
	VerifyingTests        []CodeContext     `json:"verifying_tests,omitempty"`
	RelatedDecisions      []DecisionContext `json:"related_decisions,omitempty"`
	DownstreamConsumers   []string          `json:"downstream_consumers,omitempty"`
	AnnotationConventions map[string]string `json:"annotation_conventions,omitempty"`
	PatchWorkflow         map[string]any    `json:"patch_workflow"`
	RelevantSkills        []string          `json:"relevant_skills"`
}

// TaskContext is the task summary inside a bundle.
type TaskContext struct {
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	Initiative string   `json:"initiative"`
	Stream     string   `json:"stream"`
	Verifies   []string `json:"verifies,omitempty"`
}

// ManifestContext is a feature summary inside a bundle.
type ManifestContext struct {
	FeatureID            string             `json:"feature_id"`
	Purpose              string             `json:"purpose"`
	CapabilitiesTouched  []string           `json:"capabilities_touched,omitempty"`
	InvariantsToPreserve []InvariantContext `json:"invariants_to_preserve,omitempty"`
}

// InvariantContext is an invariant the agent must preserve.
type InvariantContext struct {
	ID                  string   `json:"id"`
	Statement           string   `json:"statement"`
	CurrentlyEnforcedBy []string `json:"currently_enforced_by,omitempty"`
}

// ContractContext is a locked contract.
type ContractContext struct {
	Path     string `json:"path"`
	Content  string `json:"content,omitempty"`
	LockedAt string `json:"locked_at,omitempty"`
}

// CodeContext is one source file's relevant content.
type CodeContext struct {
	File    string `json:"file"`
	FQN     string `json:"fqn,omitempty"`
	Content string `json:"content,omitempty"`
}

// DecisionContext is a referenced ADR.
type DecisionContext struct {
	ADR     string `json:"adr"`
	Summary string `json:"summary"`
	Content string `json:"content,omitempty"`
}

// BuildAgentContext assembles the bundle for a task. If taskID is empty the
// bundle covers the whole repository at a summary level.
func BuildAgentContext(repo string, kg schema.KnowledgeGraph, taskID string) (AgentContext, error) {
	ac := AgentContext{
		PatchWorkflow: map[string]any{
			"preview_tool":          "lattice_preview_patch",
			"apply_tool":            "lattice_apply_patch",
			"atomic":                true,
			"rollback_on_violation": true,
		},
		RelevantSkills: []string{
			"lattice/working-tasks",
			"lattice/writing-annotations",
			"lattice/refactoring-with-lattice",
		},
	}

	var task *schema.Task
	for i := range kg.Tasks {
		if kg.Tasks[i].ID == taskID {
			task = &kg.Tasks[i]
		}
	}
	if taskID != "" && task == nil {
		return ac, fmt.Errorf("task %q not found", taskID)
	}

	featureSet := map[string]bool{}
	if task != nil {
		ac.Task = &TaskContext{
			ID: task.ID, Title: task.Title, Initiative: task.Initiative,
			Stream: task.Stream, Verifies: task.Verifies,
		}
		for _, ref := range task.Verifies {
			if i := strings.LastIndex(ref, ":"); i > 0 {
				featureSet[ref[:i]] = true
			}
		}
		ac.Contracts = contractsForInitiative(repo, kg, task.Initiative)
	}
	if len(featureSet) == 0 {
		for _, m := range kg.Features {
			featureSet[m.ID] = true
		}
	}

	enforcers := enforcerIndex(kg)
	for _, m := range kg.Features {
		if !featureSet[m.ID] {
			continue
		}
		mc := ManifestContext{FeatureID: m.ID, Purpose: m.Purpose}
		for _, cap := range m.Capabilities {
			mc.CapabilitiesTouched = append(mc.CapabilitiesTouched, cap.ID)
		}
		for _, inv := range m.Invariants {
			mc.InvariantsToPreserve = append(mc.InvariantsToPreserve, InvariantContext{
				ID: inv.ID, Statement: inv.Statement,
				CurrentlyEnforcedBy: enforcers[m.ID+":"+inv.ID],
			})
		}
		ac.Manifests = append(ac.Manifests, mc)
		ac.DownstreamConsumers = append(ac.DownstreamConsumers, consumersOf(m.ID, kg)...)
	}
	sort.Strings(ac.DownstreamConsumers)
	ac.DownstreamConsumers = uniq(ac.DownstreamConsumers)

	ac.CurrentCode = codeForFeatures(repo, kg, featureSet, false)
	ac.VerifyingTests = codeForFeatures(repo, kg, featureSet, true)
	ac.RelatedDecisions = decisionsForFeatures(repo, kg, featureSet)
	ac.AnnotationConventions = annotationConventions(kg, featureSet)
	return ac, nil
}

// enforcerIndex maps "feature:INV" to the symbols that enforce it.
func enforcerIndex(kg schema.KnowledgeGraph) map[string][]string {
	idx := map[string][]string{}
	for _, s := range kg.Symbols {
		for _, inv := range s.EnforcesInvariants {
			ref := inv
			if !strings.Contains(inv, ":") && s.Feature != "" {
				ref = s.Feature + ":" + inv
			}
			idx[ref] = append(idx[ref], s.FQN)
		}
	}
	return idx
}

func contractsForInitiative(repo string, kg schema.KnowledgeGraph, initiativeID string) []ContractContext {
	var out []ContractContext
	for _, in := range kg.Initiatives {
		if in.ID != initiativeID {
			continue
		}
		for _, ct := range in.Contracts {
			cc := ContractContext{Path: ct.Path, LockedAt: ct.LockedAt}
			if data, err := os.ReadFile(filepath.Join(repo, ct.Path)); err == nil {
				cc.Content = string(data)
			}
			out = append(out, cc)
		}
	}
	return out
}

func codeForFeatures(repo string, kg schema.KnowledgeGraph, features map[string]bool, tests bool) []CodeContext {
	pool := kg.Symbols
	if tests {
		pool = kg.Tests
	}
	seen := map[string]bool{}
	var out []CodeContext
	for _, s := range pool {
		if !features[s.Feature] || seen[s.File] {
			continue
		}
		seen[s.File] = true
		cc := CodeContext{File: s.File, FQN: s.FQN}
		if data, err := os.ReadFile(filepath.Join(repo, s.File)); err == nil {
			cc.Content = string(data)
		}
		out = append(out, cc)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].File < out[j].File })
	return out
}

func decisionsForFeatures(repo string, kg schema.KnowledgeGraph, features map[string]bool) []DecisionContext {
	seen := map[string]bool{}
	var out []DecisionContext
	for _, m := range kg.Features {
		if !features[m.ID] {
			continue
		}
		for _, d := range m.Decisions {
			if seen[d.ADR] {
				continue
			}
			seen[d.ADR] = true
			dc := DecisionContext{ADR: d.ADR, Summary: d.Summary}
			if content := readADR(repo, d.ADR); content != "" {
				dc.Content = content
			}
			out = append(out, dc)
		}
	}
	return out
}

// readADR looks for an ADR file under decisions/ by id prefix.
func readADR(repo, adr string) string {
	dir := filepath.Join(repo, "decisions")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), adr) {
			if data, err := os.ReadFile(filepath.Join(dir, e.Name())); err == nil {
				return string(data)
			}
		}
	}
	return ""
}

func annotationConventions(kg schema.KnowledgeGraph, features map[string]bool) map[string]string {
	lang := ""
	for _, s := range kg.Symbols {
		if features[s.Feature] {
			lang = s.Language
			break
		}
	}
	if lang == "" {
		return nil
	}
	return map[string]string{
		"language": lang,
		"rules":    "Annotate the implementing symbol; the effective set unions module, role, and inherited annotations.",
	}
}

func consumersOf(feature string, kg schema.KnowledgeGraph) []string {
	var out []string
	for _, m := range kg.Features {
		for _, dep := range m.DependsOn {
			if dep == feature {
				out = append(out, m.ID)
			}
		}
	}
	return out
}

func uniq(s []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range s {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}
