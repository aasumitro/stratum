// Package storage provides a minimal Supabase Storage client using the REST API.
package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Config holds the Supabase project URL and service role key.
type Config struct {
	BaseURL string
	Key     string // service_role JWT — used for all storage operations
}

// BucketConfig describes a Supabase Storage bucket to ensure at startup.
type BucketConfig struct {
	Name          string
	Public        bool
	FileSizeLimit int64 // bytes; 0 = Supabase project default
}

// Client wraps the Supabase Storage HTTP API.
type Client struct {
	baseURL string
	key     string
	http    *http.Client
}

func New(cfg Config) *Client {
	return &Client{
		baseURL: strings.TrimSuffix(cfg.BaseURL, "/"),
		key:     cfg.Key,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

// BaseURL returns the configured Supabase project URL (no trailing slash).
func (c *Client) BaseURL() string { return c.baseURL }

// EnsureBuckets creates each bucket if it does not already exist.
// A 409 (already exists) is treated as success so startup is idempotent.
func (c *Client) EnsureBuckets(ctx context.Context, buckets []BucketConfig) error {
	for _, b := range buckets {
		if err := c.createBucket(ctx, b); err != nil {
			return fmt.Errorf("storage.EnsureBuckets %q: %w", b.Name, err)
		}
	}
	return nil
}

func (c *Client) createBucket(ctx context.Context, b BucketConfig) error {
	body := map[string]any{
		"id":     b.Name,
		"name":   b.Name,
		"public": b.Public,
	}
	if b.FileSizeLimit > 0 {
		body["file_size_limit"] = b.FileSizeLimit
	}
	raw, _ := json.Marshal(body)

	url := fmt.Sprintf("%s/storage/v1/bucket", c.baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("storage.createBucket: build request: %w", err)
	}
	c.setAuthHeaders(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("storage.createBucket: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusConflict {
		return nil // bucket already exists
	}
	if resp.StatusCode >= http.StatusBadRequest {
		respBody, _ := io.ReadAll(resp.Body)
		if isDuplicateBucketError(respBody) {
			return nil // bucket already exists — Supabase reports this as HTTP 400
			// with a "statusCode":"409" field embedded in the JSON body, not as
			// an actual HTTP 409, so the status-code check above doesn't catch it.
		}
		return fmt.Errorf("storage.createBucket: status %d: %s", resp.StatusCode, respBody)
	}
	return nil
}

func isDuplicateBucketError(body []byte) bool {
	var parsed struct {
		StatusCode string `json:"statusCode"`
		Error      string `json:"error"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return false
	}
	return parsed.StatusCode == "409" || parsed.Error == "Duplicate"
}

// Upload upserts an object into bucket at path with the given content type.
func (c *Client) Upload(ctx context.Context, bucket, path string, r io.Reader, contentType string) error {
	url := fmt.Sprintf("%s/storage/v1/object/%s/%s", c.baseURL, bucket, path)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, r)
	if err != nil {
		return fmt.Errorf("storage.Upload: build request: %w", err)
	}
	c.setAuthHeaders(req)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("x-upsert", "true")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("storage.Upload: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("storage.Upload: status %d: %s", resp.StatusCode, b)
	}
	return nil
}

// SignedURL returns a pre-signed download URL valid for ttl.
func (c *Client) SignedURL(ctx context.Context, bucket, path string, ttl time.Duration) (string, error) {
	url := fmt.Sprintf("%s/storage/v1/object/sign/%s/%s", c.baseURL, bucket, path)

	body, _ := json.Marshal(map[string]any{"expiresIn": int(ttl.Seconds()), "download": true})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("storage.SignedURL: build request: %w", err)
	}
	c.setAuthHeaders(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("storage.SignedURL: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("storage.SignedURL: status %d: %s", resp.StatusCode, b)
	}

	var result struct {
		SignedURL string `json:"signedURL"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("storage.SignedURL: decode: %w", err)
	}
	if len(result.SignedURL) > 0 && result.SignedURL[0] == '/' {
		return c.baseURL + result.SignedURL, nil
	}
	return result.SignedURL, nil
}

// Delete removes a single object from bucket at path.
func (c *Client) Delete(ctx context.Context, bucket, path string) error {
	url := fmt.Sprintf("%s/storage/v1/object/%s", c.baseURL, bucket)

	body, _ := json.Marshal(map[string][]string{"prefixes": {path}})
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("storage.Delete: build request: %w", err)
	}
	c.setAuthHeaders(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("storage.Delete: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("storage.Delete: status %d: %s", resp.StatusCode, b)
	}
	return nil
}

func (c *Client) setAuthHeaders(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("apikey", c.key)
}
