package validate

import (
	"fmt"
	"sort"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// checkInitiativesAndTasks runs the initiative- and task-integrity rules.
func (c *corpus) checkInitiativesAndTasks() []schema.Violation {
	var v []schema.Violation

	// Contracts declared per initiative.
	contracts := map[string]map[string]bool{}
	for i := range c.kg.Initiatives {
		in := &c.kg.Initiatives[i]
		loc := &schema.Location{File: in.SourcePath}
		contracts[in.ID] = map[string]bool{}
		for _, ct := range in.Contracts {
			contracts[in.ID][ct.Path] = true
		}

		// INITIATIVE_SCHEMA.
		for _, msg := range initiativeSchemaErrors(in) {
			v = append(v, schema.Violation{
				Code: schema.CodeInitiativeSchema, Severity: schema.SeverityError,
				InitiativeID: in.ID, Message: msg, Location: loc,
			})
		}

		// INITIATIVE_MIGRATION_CONSUMER_MISSING.
		if in.Migration != nil {
			for _, ph := range in.Migration.Phases {
				for _, consumer := range ph.Consumers {
					if _, ok := c.features[consumer]; !ok {
						v = append(v, schema.Violation{
							Code: schema.CodeInitiativeMigrationConsumerMissing, Severity: schema.SeverityWarning,
							InitiativeID: in.ID, Location: loc,
							Message: fmt.Sprintf("migration phase %q lists consumer %q with no manifest", ph.ID, consumer),
						})
					}
				}
			}
		}
	}

	for i := range c.kg.Tasks {
		t := &c.kg.Tasks[i]
		loc := &schema.Location{File: t.SourcePath}

		// TASK_SCHEMA.
		for _, msg := range taskSchemaErrors(t) {
			v = append(v, schema.Violation{
				Code: schema.CodeTaskSchema, Severity: schema.SeverityError,
				TaskID: t.ID, Message: msg, Location: loc,
			})
		}

		// TASK_REFERENCES_MISSING_INITIATIVE.
		if t.Initiative != "" && !c.initOK[t.Initiative] {
			v = append(v, schema.Violation{
				Code: schema.CodeTaskReferencesMissingInitiative, Severity: schema.SeverityError,
				TaskID: t.ID, Location: loc,
				Message: fmt.Sprintf("task %s references unknown initiative %q", t.ID, t.Initiative),
			})
			continue
		}

		// TASK_REFERENCES_MISSING_STREAM.
		if t.Stream != "" && t.Initiative != "" {
			if streams, ok := c.streams[t.Initiative]; ok && !streams[t.Stream] {
				v = append(v, schema.Violation{
					Code: schema.CodeTaskReferencesMissingStream, Severity: schema.SeverityError,
					TaskID: t.ID, Location: loc,
					Message: fmt.Sprintf("task %s references stream %q not declared on initiative %q",
						t.ID, t.Stream, t.Initiative),
				})
			}
		}

		// TASK_DEPENDS_ON_MISSING_CONTRACT.
		for _, dep := range t.DependsOn {
			if dep.Contract == "" {
				continue
			}
			if cs, ok := contracts[t.Initiative]; !ok || !cs[dep.Contract] {
				v = append(v, schema.Violation{
					Code: schema.CodeTaskDependsOnMissingContract, Severity: schema.SeverityError,
					TaskID: t.ID, Location: loc,
					Message: fmt.Sprintf("task %s depends on contract %q not declared on its initiative",
						t.ID, dep.Contract),
					NextAction: &schema.NextAction{Kind: "add_contract", Ref: dep.Contract},
				})
			}
		}
	}

	v = append(v, c.taskDependencyCycles()...)
	return v
}

func initiativeSchemaErrors(in *schema.Initiative) []string {
	var errs []string
	if strings.TrimSpace(in.ID) == "" {
		errs = append(errs, "missing required field: id")
	}
	if in.Type != "" && in.Type != "initiative" {
		errs = append(errs, fmt.Sprintf("field type must be \"initiative\", got %q", in.Type))
	}
	if !validInitiativeStatus(in.Status) {
		errs = append(errs, fmt.Sprintf("invalid status %q", in.Status))
	}
	if strings.TrimSpace(in.Motivation) == "" {
		errs = append(errs, "missing required field: motivation")
	}
	return errs
}

func taskSchemaErrors(t *schema.Task) []string {
	var errs []string
	if strings.TrimSpace(t.ID) == "" {
		errs = append(errs, "missing required field: id")
	}
	if strings.TrimSpace(t.Title) == "" {
		errs = append(errs, "missing required field: title")
	}
	if strings.TrimSpace(t.Initiative) == "" {
		errs = append(errs, "missing required field: initiative")
	}
	if !validTaskStatus(t.Status) {
		errs = append(errs, fmt.Sprintf("invalid status %q", t.Status))
	}
	return errs
}

func validInitiativeStatus(s schema.InitiativeStatus) bool {
	switch s {
	case schema.InitiativeProposed, schema.InitiativeAccepted, schema.InitiativeInProgress,
		schema.InitiativePaused, schema.InitiativeComplete, schema.InitiativeCancelled:
		return true
	}
	return false
}

func validTaskStatus(s schema.TaskStatus) bool {
	switch s {
	case schema.TaskNotStarted, schema.TaskInProgress, schema.TaskBlocked,
		schema.TaskInReview, schema.TaskDone, schema.TaskCancelled:
		return true
	}
	return false
}

// taskDependencyCycles detects cycles among task-to-task dependencies.
func (c *corpus) taskDependencyCycles() []schema.Violation {
	deps := map[string][]string{}
	src := map[string]string{}
	for i := range c.kg.Tasks {
		t := &c.kg.Tasks[i]
		src[t.ID] = t.SourcePath
		for _, d := range t.DependsOn {
			if d.Task != "" {
				deps[t.ID] = append(deps[t.ID], d.Task)
			}
		}
	}

	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := map[string]int{}
	var stack []string
	reported := map[string]bool{}
	var v []schema.Violation

	var visit func(id string)
	visit = func(id string) {
		color[id] = gray
		stack = append(stack, id)
		nbrs := append([]string(nil), deps[id]...)
		sort.Strings(nbrs)
		for _, n := range nbrs {
			switch color[n] {
			case white:
				visit(n)
			case gray:
				cyc := extractCycle(stack, n)
				k := cycleKey(cyc)
				if !reported[k] {
					reported[k] = true
					v = append(v, schema.Violation{
						Code: schema.CodeTaskDependencyCycle, Severity: schema.SeverityError,
						TaskID:   cyc[0],
						Location: &schema.Location{File: src[cyc[0]]},
						Message:  "task dependency cycle: " + strings.Join(append(cyc, n), " -> "),
					})
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[id] = black
	}

	ids := make([]string, 0, len(deps))
	for id := range src {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if color[id] == white {
			visit(id)
		}
	}
	return v
}
