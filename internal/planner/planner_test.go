package planner

import (
	"testing"
	"time"
)

func TestPlanRepositoryProtectsDigestAndKeepsMinimum(t *testing.T) {
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	cutoff := now.AddDate(0, 0, -30)
	tags := []TagInfo{
		{Repository: "app", Tag: "latest", Digest: "sha256:protected", Created: now.AddDate(0, 0, -100)},
		{Repository: "app", Tag: "old-alias", Digest: "sha256:protected", Created: now.AddDate(0, 0, -100)},
		{Repository: "app", Tag: "new", Digest: "sha256:new", Created: now.AddDate(0, 0, -5)},
		{Repository: "app", Tag: "old-keep", Digest: "sha256:oldkeep", Created: now.AddDate(0, 0, -80)},
		{Repository: "app", Tag: "old-delete", Digest: "sha256:olddelete", Created: now.AddDate(0, 0, -90)},
	}

	plan := PlanRepository("app", tags, map[string]struct{}{"latest": {}}, 2, cutoff)
	candidates := plan.CandidateDigests()
	if len(candidates) != 1 || candidates[0] != "sha256:olddelete" {
		t.Fatalf("unexpected candidates: %#v", candidates)
	}

	protected := plan.ProtectedDigests()
	if len(protected) != 1 || protected[0] != "sha256:protected" {
		t.Fatalf("unexpected protected digests: %#v", protected)
	}

	minimum := plan.MinimumKeptDigests()
	if len(minimum) != 2 {
		t.Fatalf("expected two minimum-kept non-protected digests, got %#v", minimum)
	}
}
