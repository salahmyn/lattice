package breaktest

// @module-feature breaktest

import (
	"context"
	"fmt"
	"strings"
)

// restoreSQL runs break.restore after a run whose command applied the SQL
// object files, once its verifiers have finished. A runner that applies
// {patched_sql} and then runs the tests ends with the database in whatever
// state the last statement left; the restore commands put the original back.
// When one exits non-zero the database may be wrong for every later run, so
// break.setup runs again at once. The run's outcome is never changed: its
// verifiers already ran. A setup that fails then is remembered like any other
// setup failure, so the runs after it read unknown.
// @enforces breaktest:INV-18
func (r *runner) restoreSQL(ctx context.Context, sql sqlFiles, used bool) {
	cmds := r.opts.Config.Break.Restore
	if !used || len(cmds) == 0 || !sql.ok() {
		return
	}
	// A restore is not skipped for a cancelled run: leaving the database
	// wrong would poison the next one.
	ctx = context.WithoutCancel(ctx)
	repl := strings.NewReplacer(
		"{repo}", shellQuote(r.repo.Top),
		patchedSQLToken, shellQuote(sql.patched),
		originalSQLToken, shellQuote(sql.original))
	for _, c := range cmds {
		res := command(ctx, "", r.wt.Dir, r.opts.Timeout, "sh", "-c", repl.Replace(c))
		if res.status == statusOK {
			continue
		}
		why := res.reason
		if why == "" {
			why = failingLine(res.output)
		}
		fmt.Fprintf(r.opts.warn(), "break.restore failed (%s: %s); running break.setup again\n", clip(c), why)
		r.rerunSetup(ctx)
		return
	}
}

// rerunSetup runs break.setup again. The first setup's error, if any, is
// replaced by this one's.
func (r *runner) rerunSetup(ctx context.Context) {
	r.setupDone = false
	if err := r.ensureSetup(ctx); err != nil {
		r.setupErr = fmt.Errorf("setup failed after a failed restore: %s",
			strings.TrimPrefix(err.Error(), "setup failed: "))
	}
}

// healBaseline retries a failing baseline once, after a fresh setup, when a
// run since the last setup applied the SQL object files: the failure may be a
// database a restore left wrong, not the tests. The retry's result is the
// baseline: a pass proceeds normally, a failure is real and reads as before.
// The failing invocations are dropped from the run's shared results so the
// retry runs them; the baseline store ends with the retry's result only.
// @enforces breaktest:INV-18
func (r *runner) healBaseline(ctx context.Context, groups []group, sw sqlFiles) baseline {
	for _, g := range groups {
		key := r.keyOf(g)
		if inv, ok := r.shared[key]; ok && inv.failedBaseline() {
			delete(r.shared, key)
		}
	}
	r.setupDone = false
	if err := r.ensureSetup(ctx); err != nil {
		r.setupErr = fmt.Errorf("setup failed after a failed baseline: %s",
			strings.TrimPrefix(err.Error(), "setup failed: "))
		return baseline{status: BaselineError, reason: r.setupErr.Error()}
	}
	// The groups that passed the first time are taken from the run again;
	// that is not a second reuse.
	reused := r.counts.Reused
	again := r.baseline(ctx, groups, sw.asBaseline())
	r.counts.Reused = reused
	return again
}
