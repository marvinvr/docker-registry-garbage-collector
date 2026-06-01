package runner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/marvinvr/docker-registry-garbage-collector/internal/config"
	registrygc "github.com/marvinvr/docker-registry-garbage-collector/internal/gc"
	"github.com/marvinvr/docker-registry-garbage-collector/internal/planner"
	"github.com/marvinvr/docker-registry-garbage-collector/internal/registry"
)

type Runner struct {
	cfg    config.Config
	client *registry.Client
	gc     *registrygc.Executor
	logger *slog.Logger
	now    func() time.Time
}

type stats struct {
	repositoriesScanned int
	tagsInspected       int
	protectedDigests    int
	minimumKeptDigests  int
	deletionCandidates  int
	deletedManifests    int
	repositoryErrors    int
}

func New(cfg config.Config, client *registry.Client, gcExecutor *registrygc.Executor, logger *slog.Logger) *Runner {
	return &Runner{
		cfg:    cfg,
		client: client,
		gc:     gcExecutor,
		logger: logger,
		now:    time.Now,
	}
}

func (r *Runner) Run(ctx context.Context, trigger string) error {
	started := r.now().UTC()
	cutoff := started.AddDate(0, 0, -r.cfg.ThresholdDays)
	runStats := stats{}

	r.logger.Info("run started",
		"trigger", trigger,
		"registry_url", r.cfg.RegistryURL,
		"dry_run", r.cfg.DryRun,
		"threshold_days", r.cfg.ThresholdDays,
		"cutoff", cutoff.Format(time.RFC3339),
		"min_images_keep", r.cfg.MinImagesKeep,
		"protected_tags", r.cfg.ProtectedTagList(),
		"run_garbage_collect", r.cfg.RunGarbageCollect,
		"garbage_collect_dry_run", r.cfg.GarbageCollectDryRun,
	)

	if err := r.client.Ping(ctx); err != nil {
		return fmt.Errorf("registry ping failed: %w", err)
	}

	repositories, err := r.repositories(ctx)
	if err != nil {
		return err
	}
	runStats.repositoriesScanned = len(repositories)
	r.logger.Info("repositories resolved", "count", len(repositories), "repositories", repositories)

	for _, repository := range repositories {
		repoStats, err := r.processRepository(ctx, repository, cutoff)
		runStats.add(repoStats)
		if err != nil {
			runStats.repositoryErrors++
			r.logger.Error("repository skipped after errors", "repository", repository, "error", err)
		}
	}

	if r.cfg.RunGarbageCollect {
		if err := r.runGarbageCollect(ctx); err != nil {
			return err
		}
	} else {
		r.logger.Info("garbage collection skipped", "reason", "disabled")
	}

	r.logger.Info("run summary",
		"repositories_scanned", runStats.repositoriesScanned,
		"tags_inspected", runStats.tagsInspected,
		"protected_digests", runStats.protectedDigests,
		"minimum_kept_digests", runStats.minimumKeptDigests,
		"deletion_candidates", runStats.deletionCandidates,
		"deleted_manifests", runStats.deletedManifests,
		"repository_errors", runStats.repositoryErrors,
	)

	if runStats.repositoryErrors > 0 {
		return fmt.Errorf("run completed with %d repository error(s)", runStats.repositoryErrors)
	}
	return nil
}

func (r *Runner) repositories(ctx context.Context) ([]string, error) {
	if len(r.cfg.Repositories) > 0 {
		return r.cfg.Repositories, nil
	}
	repositories, err := r.client.Catalog(ctx, r.cfg.PageSize)
	if err != nil {
		return nil, fmt.Errorf("list catalog: %w", err)
	}
	sort.Strings(repositories)
	return repositories, nil
}

func (r *Runner) processRepository(ctx context.Context, repository string, cutoff time.Time) (stats, error) {
	repoStats := stats{}
	r.logger.Info("repository scan started", "repository", repository)
	tags, err := r.client.Tags(ctx, repository, r.cfg.PageSize)
	if err != nil {
		return repoStats, fmt.Errorf("list tags: %w", err)
	}
	sort.Strings(tags)
	repoStats.tagsInspected = len(tags)
	r.logger.Info("repository tags resolved", "repository", repository, "tag_count", len(tags))
	if len(tags) == 0 {
		r.logger.Info("repository has no tags", "repository", repository)
		return repoStats, nil
	}

	tagInfos := make([]planner.TagInfo, 0, len(tags))
	createdCache := make(map[string]time.Time)
	var tagErrors []error

	for _, tag := range tags {
		manifest, err := r.client.GetManifest(ctx, repository, tag)
		if err != nil {
			tagErrors = append(tagErrors, fmt.Errorf("%s: resolve manifest: %w", tag, err))
			r.logger.Error("tag metadata failed", "repository", repository, "tag", tag, "error", err)
			continue
		}

		created, ok := createdCache[manifest.Digest]
		if !ok {
			created, err = r.client.ImageCreated(ctx, repository, manifest)
			if err != nil {
				tagErrors = append(tagErrors, fmt.Errorf("%s: image metadata: %w", tag, err))
				r.logger.Error("tag image metadata failed", "repository", repository, "tag", tag, "digest", manifest.Digest, "error", err)
				continue
			}
			createdCache[manifest.Digest] = created
		}

		tagInfos = append(tagInfos, planner.TagInfo{
			Repository: repository,
			Tag:        tag,
			Digest:     manifest.Digest,
			MediaType:  manifest.MediaType,
			Created:    created,
		})
	}

	if len(tagErrors) > 0 {
		return repoStats, fmt.Errorf("metadata failed for %d tag(s): %w", len(tagErrors), errors.Join(tagErrors...))
	}

	plan := planner.PlanRepository(repository, tagInfos, r.cfg.ProtectedTags, r.cfg.MinImagesKeep, cutoff)
	repoStats.protectedDigests = len(plan.ProtectedDigests())
	repoStats.minimumKeptDigests = len(plan.MinimumKeptDigests())
	repoStats.deletionCandidates = len(plan.CandidateDigests())

	r.logPlan(plan)

	if r.cfg.DryRun {
		for _, candidate := range plan.Candidates() {
			r.logger.Info("manifest delete skipped",
				"repository", candidate.Repository,
				"digest", candidate.Digest,
				"tags", candidate.Tags,
				"created", candidate.Created.Format(time.RFC3339),
				"reason", "dry-run",
			)
		}
		return repoStats, nil
	}

	for _, candidate := range plan.Candidates() {
		if err := r.client.DeleteManifest(ctx, candidate.Repository, candidate.Digest); err != nil {
			return repoStats, fmt.Errorf("delete manifest %s: %w", candidate.Digest, err)
		}
		repoStats.deletedManifests++
		r.logger.Info("manifest deleted",
			"repository", candidate.Repository,
			"digest", candidate.Digest,
			"tags", candidate.Tags,
			"created", candidate.Created.Format(time.RFC3339),
		)
	}

	return repoStats, nil
}

func (r *Runner) logPlan(plan planner.RepositoryPlan) {
	protectedDigests := plan.ProtectedDigests()
	minimumKeptDigests := plan.MinimumKeptDigests()
	retainedDigests := plan.RetainedDigests()
	candidateDigests := plan.CandidateDigests()

	r.logger.Info("repository plan complete",
		"repository", plan.Repository,
		"digests_total", len(plan.Digests),
		"protected_digest_count", len(protectedDigests),
		"minimum_kept_digest_count", len(minimumKeptDigests),
		"retained_digest_count", len(retainedDigests),
		"deletion_candidate_count", len(candidateDigests),
	)
	if len(protectedDigests) > 0 {
		r.logger.Info("protected digests", "repository", plan.Repository, "digests", protectedDigests)
	}
	if len(minimumKeptDigests) > 0 {
		r.logger.Info("minimum kept digests", "repository", plan.Repository, "digests", minimumKeptDigests)
	}
	for _, digest := range plan.Digests {
		r.logger.Log(context.Background(), slog.LevelDebug, "digest plan",
			"repository", digest.Repository,
			"digest", digest.Digest,
			"tags", digest.Tags,
			"created", digest.Created.Format(time.RFC3339),
			"protected", digest.Protected,
			"kept_by_minimum", digest.KeptByMinimum,
			"candidate", digest.Candidate,
			"reason", digest.Reason,
			"media_type", digest.MediaType,
		)
	}
}

func (r *Runner) runGarbageCollect(ctx context.Context) error {
	r.logger.Info("garbage collection started",
		"config_path", r.cfg.RegistryConfigPath,
		"dry_run", r.cfg.GarbageCollectDryRun,
		"delete_untagged", r.cfg.GarbageCollectDeleteUntagged,
	)
	output, err := r.gc.Run(ctx, registrygc.Options{
		ConfigPath:     r.cfg.RegistryConfigPath,
		DryRun:         r.cfg.GarbageCollectDryRun,
		DeleteUntagged: r.cfg.GarbageCollectDeleteUntagged,
	})
	if err != nil {
		r.logger.Error("garbage collection failed", "output", output, "error", err)
		return err
	}
	r.logger.Info("garbage collection finished", "dry_run", r.cfg.GarbageCollectDryRun, "output", output)
	return nil
}

func (s *stats) add(other stats) {
	s.tagsInspected += other.tagsInspected
	s.protectedDigests += other.protectedDigests
	s.minimumKeptDigests += other.minimumKeptDigests
	s.deletionCandidates += other.deletionCandidates
	s.deletedManifests += other.deletedManifests
}
