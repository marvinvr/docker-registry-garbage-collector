package registry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	MediaTypeDockerManifestV2     = "application/vnd.docker.distribution.manifest.v2+json"
	MediaTypeDockerManifestListV2 = "application/vnd.docker.distribution.manifest.list.v2+json"
	MediaTypeDockerManifestV1     = "application/vnd.docker.distribution.manifest.v1+json"
	MediaTypeOCIManifest          = "application/vnd.oci.image.manifest.v1+json"
	MediaTypeOCIIndex             = "application/vnd.oci.image.index.v1+json"
)

const manifestAcceptHeader = MediaTypeDockerManifestV2 + ", " +
	MediaTypeDockerManifestListV2 + ", " +
	MediaTypeOCIManifest + ", " +
	MediaTypeOCIIndex + ", " +
	MediaTypeDockerManifestV1

type ClientConfig struct {
	BaseURL  string
	Username string
	Password string
	Token    string
	Timeout  time.Duration
}

type Client struct {
	baseURL    *url.URL
	httpClient *http.Client
	username   string
	password   string
	token      string
}

type HTTPError struct {
	Method     string
	URL        string
	StatusCode int
	Body       string
}

func (e *HTTPError) Error() string {
	body := strings.TrimSpace(e.Body)
	if body == "" {
		return fmt.Sprintf("%s %s returned HTTP %d", e.Method, e.URL, e.StatusCode)
	}
	return fmt.Sprintf("%s %s returned HTTP %d: %s", e.Method, e.URL, e.StatusCode, body)
}

type Manifest struct {
	Digest    string
	MediaType string
	Content   []byte
}

func NewClient(cfg ClientConfig) (*Client, error) {
	baseURL, err := url.Parse(cfg.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse registry URL: %w", err)
	}
	if baseURL.Scheme == "" || baseURL.Host == "" {
		return nil, fmt.Errorf("registry URL must include scheme and host")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: cfg.Timeout,
		},
		username: cfg.Username,
		password: cfg.Password,
		token:    cfg.Token,
	}, nil
}

func (c *Client) Ping(ctx context.Context) error {
	req, err := c.newRequest(ctx, http.MethodGet, c.apiURL("/v2/", nil), nil)
	if err != nil {
		return err
	}
	_, _, err = c.do(req, http.StatusOK)
	return err
}

func (c *Client) Catalog(ctx context.Context, pageSize int) ([]string, error) {
	query := url.Values{}
	if pageSize > 0 {
		query.Set("n", fmt.Sprintf("%d", pageSize))
	}
	nextURL := c.apiURL("/v2/_catalog", query)
	var repositories []string
	for nextURL != "" {
		req, err := c.newRequest(ctx, http.MethodGet, nextURL, nil)
		if err != nil {
			return nil, err
		}
		resp, body, err := c.do(req, http.StatusOK)
		if err != nil {
			return nil, err
		}
		var page struct {
			Repositories []string `json:"repositories"`
		}
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, fmt.Errorf("decode catalog response: %w", err)
		}
		repositories = append(repositories, page.Repositories...)
		nextURL = c.nextLink(resp.Header.Get("Link"))
	}
	return repositories, nil
}

func (c *Client) Tags(ctx context.Context, repository string, pageSize int) ([]string, error) {
	query := url.Values{}
	if pageSize > 0 {
		query.Set("n", fmt.Sprintf("%d", pageSize))
	}
	nextURL := c.apiURL("/v2/"+escapeRepository(repository)+"/tags/list", query)
	var tags []string
	for nextURL != "" {
		req, err := c.newRequest(ctx, http.MethodGet, nextURL, nil)
		if err != nil {
			return nil, err
		}
		resp, body, err := c.do(req, http.StatusOK)
		if err != nil {
			return nil, err
		}
		var page struct {
			Tags []string `json:"tags"`
		}
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, fmt.Errorf("decode tags response for %s: %w", repository, err)
		}
		tags = append(tags, page.Tags...)
		nextURL = c.nextLink(resp.Header.Get("Link"))
	}
	return tags, nil
}

func (c *Client) GetManifest(ctx context.Context, repository string, reference string) (Manifest, error) {
	endpoint := c.apiURL("/v2/"+escapeRepository(repository)+"/manifests/"+url.PathEscape(reference), nil)
	req, err := c.newRequest(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Manifest{}, err
	}
	req.Header.Set("Accept", manifestAcceptHeader)

	resp, body, err := c.do(req, http.StatusOK)
	if err != nil {
		return Manifest{}, err
	}

	mediaType := resp.Header.Get("Content-Type")
	if parsed, _, err := mime.ParseMediaType(mediaType); err == nil {
		mediaType = parsed
	}

	digest := resp.Header.Get("Docker-Content-Digest")
	if digest == "" {
		sum := sha256.Sum256(body)
		digest = "sha256:" + hex.EncodeToString(sum[:])
	}

	return Manifest{
		Digest:    digest,
		MediaType: mediaType,
		Content:   body,
	}, nil
}

func (c *Client) GetBlob(ctx context.Context, repository string, digest string) ([]byte, error) {
	endpoint := c.apiURL("/v2/"+escapeRepository(repository)+"/blobs/"+url.PathEscape(digest), nil)
	req, err := c.newRequest(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	_, body, err := c.do(req, http.StatusOK)
	return body, err
}

func (c *Client) DeleteManifest(ctx context.Context, repository string, digest string) error {
	endpoint := c.apiURL("/v2/"+escapeRepository(repository)+"/manifests/"+url.PathEscape(digest), nil)
	req, err := c.newRequest(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return err
	}
	_, _, err = c.do(req, http.StatusAccepted)
	return err
}

func (c *Client) newRequest(ctx context.Context, method string, endpoint string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.absoluteURL(endpoint), body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "docker-registry-gc")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	} else if c.username != "" {
		req.SetBasicAuth(c.username, c.password)
	}
	return req, nil
}

func (c *Client) do(req *http.Request, expectedStatus int) (*http.Response, []byte, error) {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp, nil, err
	}
	if resp.StatusCode != expectedStatus {
		return resp, body, &HTTPError{
			Method:     req.Method,
			URL:        req.URL.String(),
			StatusCode: resp.StatusCode,
			Body:       string(body),
		}
	}
	return resp, body, nil
}

func (c *Client) apiURL(path string, query url.Values) string {
	u := *c.baseURL
	basePath := strings.TrimRight(u.Path, "/")
	u.Path = basePath + path
	u.RawQuery = query.Encode()
	return u.String()
}

func (c *Client) absoluteURL(endpoint string) string {
	if strings.HasPrefix(endpoint, "http://") || strings.HasPrefix(endpoint, "https://") {
		return endpoint
	}
	u := *c.baseURL
	if strings.HasPrefix(endpoint, "/") {
		if parsed, err := url.Parse(endpoint); err == nil {
			u.Path = parsed.Path
			u.RawQuery = parsed.RawQuery
			u.Fragment = ""
			return u.String()
		}
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/" + strings.TrimLeft(endpoint, "/")
	return u.String()
}

func (c *Client) nextLink(header string) string {
	if header == "" {
		return ""
	}
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if !strings.Contains(part, `rel="next"`) {
			continue
		}
		start := strings.Index(part, "<")
		end := strings.Index(part, ">")
		if start < 0 || end <= start {
			continue
		}
		return c.absoluteURL(part[start+1 : end])
	}
	return ""
}

func escapeRepository(repository string) string {
	parts := strings.Split(repository, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return strings.Join(parts, "/")
}
