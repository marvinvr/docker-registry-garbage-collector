package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	defaultPageSize     = 100
	defaultHTTPTimeout  = 30 * time.Second
	defaultRegistryTool = "registry"
)

type Config struct {
	RegistryURL      string
	RegistryUsername string
	RegistryPassword string
	RegistryToken    string

	CronSchedule  string
	ThresholdDays int
	MinImagesKeep int

	ProtectedTags map[string]struct{}
	Repositories  []string
	PageSize      int

	DryRun     bool
	RunOnStart bool
	RunOnce    bool

	LogLevel string

	RegistryConfigPath           string
	RegistryBinary               string
	RunGarbageCollect            bool
	GarbageCollectDryRun         bool
	GarbageCollectDeleteUntagged bool
	RegistryReadOnly             bool

	HTTPTimeout time.Duration
}

type lookupFunc func(string) (string, bool)

func Load() (Config, error) {
	return LoadFromEnv(os.LookupEnv)
}

func LoadFromEnv(lookup lookupFunc) (Config, error) {
	var errs []error

	registryURL, err := requiredString(lookup, "REGISTRY_URL")
	if err != nil {
		errs = append(errs, err)
	}
	registryURL, err = normalizeRegistryURL(registryURL)
	if err != nil {
		errs = append(errs, err)
	}

	runOnce, err := boolEnvDefault(lookup, "RUN_ONCE", false)
	if err != nil {
		errs = append(errs, err)
	}

	cronSchedule := strings.TrimSpace(value(lookup, "CRON_SCHEDULE"))
	if cronSchedule == "" && !runOnce {
		errs = append(errs, errors.New("CRON_SCHEDULE is required unless RUN_ONCE=true"))
	}

	thresholdDays, err := requiredPositiveInt(lookup, "THRESHOLD_DAYS")
	if err != nil {
		errs = append(errs, err)
	}

	minImagesKeep, err := requiredNonNegativeInt(lookup, "MIN_IMAGES_KEEP")
	if err != nil {
		errs = append(errs, err)
	}

	pageSize, err := intEnvDefault(lookup, "PAGE_SIZE", defaultPageSize)
	if err != nil {
		errs = append(errs, err)
	} else if pageSize <= 0 {
		errs = append(errs, errors.New("PAGE_SIZE must be greater than 0"))
	}

	dryRun, err := boolEnvDefault(lookup, "DRY_RUN", false)
	if err != nil {
		errs = append(errs, err)
	}

	runOnStart, err := boolEnvDefault(lookup, "RUN_ON_START", true)
	if err != nil {
		errs = append(errs, err)
	}

	runGC, err := boolEnvDefault(lookup, "RUN_GARBAGE_COLLECT", false)
	if err != nil {
		errs = append(errs, err)
	}

	gcDryRunDefault := dryRun
	gcDryRun, err := boolEnvDefault(lookup, "GARBAGE_COLLECT_DRY_RUN", gcDryRunDefault)
	if err != nil {
		errs = append(errs, err)
	}

	gcDeleteUntagged, err := boolEnvDefault(lookup, "GARBAGE_COLLECT_DELETE_UNTAGGED", true)
	if err != nil {
		errs = append(errs, err)
	}

	registryReadOnly, err := boolEnvDefault(lookup, "REGISTRY_READ_ONLY", false)
	if err != nil {
		errs = append(errs, err)
	}

	httpTimeout, err := durationEnvDefault(lookup, "HTTP_TIMEOUT", defaultHTTPTimeout)
	if err != nil {
		errs = append(errs, err)
	}

	username := strings.TrimSpace(value(lookup, "REGISTRY_USERNAME"))
	password := value(lookup, "REGISTRY_PASSWORD")
	token := strings.TrimSpace(value(lookup, "REGISTRY_TOKEN"))
	if token != "" && (username != "" || password != "") {
		errs = append(errs, errors.New("REGISTRY_TOKEN cannot be combined with REGISTRY_USERNAME or REGISTRY_PASSWORD"))
	}
	if (username == "") != (password == "") {
		errs = append(errs, errors.New("REGISTRY_USERNAME and REGISTRY_PASSWORD must be set together"))
	}

	registryConfigPath := strings.TrimSpace(value(lookup, "REGISTRY_CONFIG_PATH"))
	if runGC && registryConfigPath == "" {
		errs = append(errs, errors.New("REGISTRY_CONFIG_PATH is required when RUN_GARBAGE_COLLECT=true"))
	}
	if runGC && !gcDryRun && !registryReadOnly {
		errs = append(errs, errors.New("REGISTRY_READ_ONLY=true is required before real garbage collection can run"))
	}

	if len(errs) > 0 {
		return Config{}, errors.Join(errs...)
	}

	protectedTags := parseSet(value(lookup, "PROTECTED_TAGS"))
	protectedTags["latest"] = struct{}{}

	return Config{
		RegistryURL:                  registryURL,
		RegistryUsername:             username,
		RegistryPassword:             password,
		RegistryToken:                token,
		CronSchedule:                 cronSchedule,
		ThresholdDays:                thresholdDays,
		MinImagesKeep:                minImagesKeep,
		ProtectedTags:                protectedTags,
		Repositories:                 parseList(value(lookup, "REPOSITORIES")),
		PageSize:                     pageSize,
		DryRun:                       dryRun,
		RunOnStart:                   runOnStart,
		RunOnce:                      runOnce,
		LogLevel:                     defaultString(value(lookup, "LOG_LEVEL"), "info"),
		RegistryConfigPath:           registryConfigPath,
		RegistryBinary:               defaultString(value(lookup, "REGISTRY_BINARY"), defaultRegistryTool),
		RunGarbageCollect:            runGC,
		GarbageCollectDryRun:         gcDryRun,
		GarbageCollectDeleteUntagged: gcDeleteUntagged,
		RegistryReadOnly:             registryReadOnly,
		HTTPTimeout:                  httpTimeout,
	}, nil
}

func (c Config) ProtectedTagList() []string {
	tags := make([]string, 0, len(c.ProtectedTags))
	for tag := range c.ProtectedTags {
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	return tags
}

func requiredString(lookup lookupFunc, name string) (string, error) {
	raw := strings.TrimSpace(value(lookup, name))
	if raw == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	return raw, nil
}

func requiredPositiveInt(lookup lookupFunc, name string) (int, error) {
	value, err := requiredNonNegativeInt(lookup, name)
	if err != nil {
		return 0, err
	}
	if value <= 0 {
		return 0, fmt.Errorf("%s must be greater than 0", name)
	}
	return value, nil
}

func requiredNonNegativeInt(lookup lookupFunc, name string) (int, error) {
	raw := strings.TrimSpace(value(lookup, name))
	if raw == "" {
		return 0, fmt.Errorf("%s is required", name)
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", name, err)
	}
	if parsed < 0 {
		return 0, fmt.Errorf("%s must be 0 or greater", name)
	}
	return parsed, nil
}

func intEnvDefault(lookup lookupFunc, name string, fallback int) (int, error) {
	raw := strings.TrimSpace(value(lookup, name))
	if raw == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", name, err)
	}
	return parsed, nil
}

func boolEnvDefault(lookup lookupFunc, name string, fallback bool) (bool, error) {
	raw := strings.TrimSpace(value(lookup, name))
	if raw == "" {
		return fallback, nil
	}
	switch strings.ToLower(raw) {
	case "1", "t", "true", "y", "yes", "on":
		return true, nil
	case "0", "f", "false", "n", "no", "off":
		return false, nil
	default:
		return false, fmt.Errorf("%s must be a boolean", name)
	}
}

func durationEnvDefault(lookup lookupFunc, name string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(value(lookup, name))
	if raw == "" {
		return fallback, nil
	}
	duration, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be a Go duration such as 30s or 2m: %w", name, err)
	}
	if duration <= 0 {
		return 0, fmt.Errorf("%s must be greater than 0", name)
	}
	return duration, nil
}

func normalizeRegistryURL(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("REGISTRY_URL is invalid: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", errors.New("REGISTRY_URL must use http or https")
	}
	if parsed.Host == "" {
		return "", errors.New("REGISTRY_URL must include a host")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return strings.TrimRight(parsed.String(), "/"), nil
}

func parseList(raw string) []string {
	parts := strings.Split(raw, ",")
	values := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		value := strings.TrimSpace(part)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		values = append(values, value)
	}
	sort.Strings(values)
	return values
}

func parseSet(raw string) map[string]struct{} {
	values := parseList(raw)
	set := make(map[string]struct{}, len(values)+1)
	for _, value := range values {
		set[value] = struct{}{}
	}
	return set
}

func defaultString(raw string, fallback string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback
	}
	return raw
}

func value(lookup lookupFunc, name string) string {
	value, _ := lookup(name)
	return value
}
