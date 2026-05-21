package analyze

import (
	"fmt"
	"strings"

	"github.com/salahmyn/lattice/pkg/lattice/schema"
)

// thresholds carries the configured similarity cutoffs.
type thresholds struct {
	warn      float64
	duplicate float64
}

// semanticChecks runs the embedding-based overlap checks.
func semanticChecks(proposal schema.Manifest, corpus []schema.Manifest, emb Embedder, th thresholds) []Finding {
	var findings []Finding
	findings = append(findings, capabilityOverlap(proposal, corpus, emb, th)...)
	findings = append(findings, invariantRestatement(proposal, corpus, emb, th)...)
	findings = append(findings, purposeOverlap(proposal, corpus, emb, th)...)

	if len(findings) == 0 {
		findings = append(findings, Finding{Level: LevelOK, Code: "NO_SEMANTIC_OVERLAP",
			Message: "no semantic overlap above threshold"})
	}
	return findings
}

func capabilityText(c schema.Capability) string {
	return c.Summary + " " + strings.Join(c.Rules, " ")
}

func capabilityOverlap(proposal schema.Manifest, corpus []schema.Manifest, emb Embedder, th thresholds) []Finding {
	type existing struct {
		ref string
		vec []float64
	}
	var pool []existing
	for _, m := range corpus {
		if m.ID == proposal.ID {
			continue
		}
		for _, c := range m.Capabilities {
			pool = append(pool, existing{ref: m.ID + ":" + c.ID, vec: emb.Embed(capabilityText(c))})
		}
	}

	var findings []Finding
	for _, c := range proposal.Capabilities {
		v := emb.Embed(capabilityText(c))
		bestRef, best := "", 0.0
		for _, e := range pool {
			if s := Cosine(v, e.vec); s > best {
				best, bestRef = s, e.ref
			}
		}
		if best >= th.warn {
			findings = append(findings, similarityFinding("CAPABILITY_OVERLAP",
				fmt.Sprintf("proposed capability %q", c.ID), bestRef, best, th))
		}
	}
	return findings
}

func invariantRestatement(proposal schema.Manifest, corpus []schema.Manifest, emb Embedder, th thresholds) []Finding {
	type existing struct {
		ref string
		vec []float64
	}
	var pool []existing
	for _, m := range corpus {
		if m.ID == proposal.ID {
			continue
		}
		for _, inv := range m.Invariants {
			pool = append(pool, existing{ref: m.ID + ":" + inv.ID, vec: emb.Embed(inv.Statement)})
		}
	}

	var findings []Finding
	for _, inv := range proposal.Invariants {
		v := emb.Embed(inv.Statement)
		bestRef, best := "", 0.0
		for _, e := range pool {
			if s := Cosine(v, e.vec); s > best {
				best, bestRef = s, e.ref
			}
		}
		if best >= th.warn {
			findings = append(findings, similarityFinding("INVARIANT_RESTATEMENT",
				fmt.Sprintf("proposed invariant %q", inv.ID), bestRef, best, th))
		}
	}
	return findings
}

func purposeOverlap(proposal schema.Manifest, corpus []schema.Manifest, emb Embedder, th thresholds) []Finding {
	v := emb.Embed(proposal.Purpose)
	bestRef, best := "", 0.0
	for _, m := range corpus {
		if m.ID == proposal.ID {
			continue
		}
		if s := Cosine(v, emb.Embed(m.Purpose)); s > best {
			best, bestRef = s, m.ID
		}
	}
	if best >= th.warn {
		return []Finding{similarityFinding("FEATURE_PURPOSE_OVERLAP",
			"proposed feature purpose", bestRef, best, th)}
	}
	return nil
}

// similarityFinding builds a finding for a similarity hit, escalating to a
// likely-duplicate warning past the duplicate threshold.
func similarityFinding(code, subject, against string, score float64, th thresholds) Finding {
	msg := fmt.Sprintf("%s overlaps %s (similarity %.2f)", subject, against, score)
	detail := map[string]interface{}{"against": against, "similarity": round2(score)}
	if score >= th.duplicate {
		detail["likely_duplicate"] = true
		msg = fmt.Sprintf("%s is likely a duplicate of %s (similarity %.2f)", subject, against, score)
	}
	return Finding{Level: LevelWarning, Code: code, Message: msg, Detail: detail}
}

func round2(f float64) float64 {
	return float64(int(f*100+0.5)) / 100
}
