package planner

import (
	"sort"
	"time"
)

type TagInfo struct {
	Repository string
	Tag        string
	Digest     string
	MediaType  string
	Created    time.Time
}

type DigestPlan struct {
	Repository    string
	Digest        string
	Tags          []string
	MediaType     string
	Created       time.Time
	Protected     bool
	KeptByMinimum bool
	Candidate     bool
	Reason        string
}

type RepositoryPlan struct {
	Repository string
	Digests    []DigestPlan
}

func PlanRepository(repository string, tags []TagInfo, protectedTags map[string]struct{}, minImagesKeep int, cutoff time.Time) RepositoryPlan {
	groups := make(map[string]*DigestPlan)
	for _, tag := range tags {
		group := groups[tag.Digest]
		if group == nil {
			group = &DigestPlan{
				Repository: repository,
				Digest:     tag.Digest,
				Created:    tag.Created,
				MediaType:  tag.MediaType,
			}
			groups[tag.Digest] = group
		}
		group.Tags = append(group.Tags, tag.Tag)
		if tag.Created.After(group.Created) {
			group.Created = tag.Created
		}
		if group.MediaType == "" {
			group.MediaType = tag.MediaType
		}
		if _, ok := protectedTags[tag.Tag]; ok {
			group.Protected = true
		}
	}

	digests := make([]DigestPlan, 0, len(groups))
	for _, group := range groups {
		sort.Strings(group.Tags)
		digests = append(digests, *group)
	}

	sortDigestPlans(digests)

	remaining := make([]int, 0, len(digests))
	for index := range digests {
		if digests[index].Protected {
			digests[index].Reason = "protected-tag"
			continue
		}
		remaining = append(remaining, index)
	}

	for keepIndex, digestIndex := range remaining {
		if keepIndex < minImagesKeep {
			digests[digestIndex].KeptByMinimum = true
			digests[digestIndex].Reason = "minimum-keep"
			continue
		}
		if digests[digestIndex].Created.IsZero() {
			digests[digestIndex].Reason = "missing-created"
			continue
		}
		if digests[digestIndex].Created.Before(cutoff) {
			digests[digestIndex].Candidate = true
			digests[digestIndex].Reason = "older-than-threshold"
			continue
		}
		digests[digestIndex].Reason = "within-threshold"
	}

	return RepositoryPlan{
		Repository: repository,
		Digests:    digests,
	}
}

func (p RepositoryPlan) Candidates() []DigestPlan {
	candidates := make([]DigestPlan, 0)
	for _, digest := range p.Digests {
		if digest.Candidate {
			candidates = append(candidates, digest)
		}
	}
	return candidates
}

func (p RepositoryPlan) CandidateDigests() []string {
	return digestList(p.Digests, func(plan DigestPlan) bool { return plan.Candidate })
}

func (p RepositoryPlan) ProtectedDigests() []string {
	return digestList(p.Digests, func(plan DigestPlan) bool { return plan.Protected })
}

func (p RepositoryPlan) MinimumKeptDigests() []string {
	return digestList(p.Digests, func(plan DigestPlan) bool { return plan.KeptByMinimum })
}

func (p RepositoryPlan) RetainedDigests() []string {
	return digestList(p.Digests, func(plan DigestPlan) bool { return !plan.Candidate })
}

func digestList(digests []DigestPlan, include func(DigestPlan) bool) []string {
	values := make([]string, 0)
	for _, digest := range digests {
		if include(digest) {
			values = append(values, digest.Digest)
		}
	}
	sort.Strings(values)
	return values
}

func sortDigestPlans(digests []DigestPlan) {
	sort.Slice(digests, func(i, j int) bool {
		left := digests[i]
		right := digests[j]
		if !left.Created.Equal(right.Created) {
			return left.Created.After(right.Created)
		}
		return left.Digest < right.Digest
	})
}
