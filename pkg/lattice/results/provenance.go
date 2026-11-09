package results

// @module-feature results

import (
	"os"
	"strings"
)

// LocalRun is the CI run id of evidence produced outside CI.
const LocalRun = "local"

// ciRunVars are the environment variables CI systems set to a run's id, in
// the order they are tried.
var ciRunVars = []string{
	"GITHUB_RUN_ID", "CI_PIPELINE_ID", "BUILDKITE_BUILD_ID", "CIRCLE_WORKFLOW_ID",
	"CI_JOB_ID", "BUILD_ID", "TF_BUILD_BUILDID",
}

// CIRunID names the CI run this process is part of, from the environment
// variables CI systems set (GITHUB_RUN_ID, CI_PIPELINE_ID and similar), or
// LocalRun when none is set. It stamps ingested evidence so that
// `lattice check --ci` can tell a CI run from a local one.
// @enforces results:INV-6
func CIRunID() string {
	for _, v := range ciRunVars {
		if id := strings.TrimSpace(os.Getenv(v)); id != "" {
			return id
		}
	}
	return LocalRun
}
