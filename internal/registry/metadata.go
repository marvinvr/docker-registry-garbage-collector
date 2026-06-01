package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

const annotationCreated = "org.opencontainers.image.created"

type descriptor struct {
	MediaType   string            `json:"mediaType"`
	Digest      string            `json:"digest"`
	Size        int64             `json:"size"`
	Annotations map[string]string `json:"annotations"`
}

type manifestDocument struct {
	SchemaVersion int               `json:"schemaVersion"`
	MediaType     string            `json:"mediaType"`
	Config        descriptor        `json:"config"`
	Layers        []descriptor      `json:"layers"`
	Manifests     []descriptor      `json:"manifests"`
	Annotations   map[string]string `json:"annotations"`
	History       []struct {
		V1Compatibility string `json:"v1Compatibility"`
	} `json:"history"`
}

func (c *Client) ImageCreated(ctx context.Context, repository string, manifest Manifest) (time.Time, error) {
	return c.imageCreated(ctx, repository, manifest, map[string]struct{}{})
}

func (c *Client) imageCreated(ctx context.Context, repository string, manifest Manifest, visited map[string]struct{}) (time.Time, error) {
	if manifest.Digest != "" {
		if _, ok := visited[manifest.Digest]; ok {
			return time.Time{}, fmt.Errorf("manifest reference cycle at %s", manifest.Digest)
		}
		visited[manifest.Digest] = struct{}{}
	}

	var doc manifestDocument
	if err := json.Unmarshal(manifest.Content, &doc); err != nil {
		return time.Time{}, fmt.Errorf("decode manifest %s: %w", manifest.Digest, err)
	}

	mediaType := doc.MediaType
	if mediaType == "" {
		mediaType = manifest.MediaType
	}

	if created, ok := parseCreatedAnnotation(doc.Annotations); ok && len(doc.Manifests) == 0 && doc.Config.Digest == "" {
		return created, nil
	}

	if isIndexManifest(mediaType, doc) {
		return c.indexCreated(ctx, repository, doc, visited)
	}

	if doc.Config.Digest != "" {
		return c.configCreated(ctx, repository, doc.Config.Digest)
	}

	if len(doc.History) > 0 {
		if created, ok := schema1Created(doc); ok {
			return created, nil
		}
	}

	return time.Time{}, fmt.Errorf("manifest %s has no supported image config", manifest.Digest)
}

func (c *Client) indexCreated(ctx context.Context, repository string, doc manifestDocument, visited map[string]struct{}) (time.Time, error) {
	var newest time.Time
	for _, child := range doc.Manifests {
		if child.Digest == "" {
			continue
		}
		childManifest, err := c.GetManifest(ctx, repository, child.Digest)
		if err != nil {
			return time.Time{}, fmt.Errorf("fetch child manifest %s: %w", child.Digest, err)
		}
		created, err := c.imageCreated(ctx, repository, childManifest, visited)
		if err != nil {
			return time.Time{}, fmt.Errorf("read child manifest %s metadata: %w", child.Digest, err)
		}
		if created.After(newest) {
			newest = created
		}
	}
	if !newest.IsZero() {
		return newest, nil
	}
	if created, ok := parseCreatedAnnotation(doc.Annotations); ok {
		return created, nil
	}
	return time.Time{}, fmt.Errorf("index manifest has no child creation timestamps")
}

func (c *Client) configCreated(ctx context.Context, repository string, configDigest string) (time.Time, error) {
	body, err := c.GetBlob(ctx, repository, configDigest)
	if err != nil {
		return time.Time{}, fmt.Errorf("fetch config blob %s: %w", configDigest, err)
	}
	var imageConfig struct {
		Created string `json:"created"`
		History []struct {
			Created string `json:"created"`
		} `json:"history"`
	}
	if err := json.Unmarshal(body, &imageConfig); err != nil {
		return time.Time{}, fmt.Errorf("decode config blob %s: %w", configDigest, err)
	}
	if imageConfig.Created != "" {
		return parseCreatedTime(imageConfig.Created)
	}
	var newest time.Time
	for _, history := range imageConfig.History {
		if history.Created == "" {
			continue
		}
		created, err := parseCreatedTime(history.Created)
		if err != nil {
			return time.Time{}, err
		}
		if created.After(newest) {
			newest = created
		}
	}
	if !newest.IsZero() {
		return newest, nil
	}
	return time.Time{}, fmt.Errorf("config blob %s has no created timestamp", configDigest)
}

func isIndexManifest(mediaType string, doc manifestDocument) bool {
	return mediaType == MediaTypeDockerManifestListV2 ||
		mediaType == MediaTypeOCIIndex ||
		(len(doc.Manifests) > 0 && doc.Config.Digest == "")
}

func parseCreatedAnnotation(annotations map[string]string) (time.Time, bool) {
	if annotations == nil {
		return time.Time{}, false
	}
	raw := annotations[annotationCreated]
	if raw == "" {
		return time.Time{}, false
	}
	created, err := parseCreatedTime(raw)
	if err != nil {
		return time.Time{}, false
	}
	return created, true
}

func schema1Created(doc manifestDocument) (time.Time, bool) {
	for _, history := range doc.History {
		if history.V1Compatibility == "" {
			continue
		}
		var compatibility struct {
			Created string `json:"created"`
		}
		if err := json.Unmarshal([]byte(history.V1Compatibility), &compatibility); err != nil {
			continue
		}
		if compatibility.Created == "" {
			continue
		}
		created, err := parseCreatedTime(compatibility.Created)
		if err != nil {
			continue
		}
		return created, true
	}
	return time.Time{}, false
}

func parseCreatedTime(raw string) (time.Time, error) {
	created, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse created timestamp %q: %w", raw, err)
	}
	return created.UTC(), nil
}
