package audit

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/md5" //nolint:gosec // S3 requires Content-MD5 on a locked upload; it is an integrity check, not a signature
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

const (
	lockModeCompliance = "COMPLIANCE"

	amzDateFormat = "20060102T150405Z"
	headerAMZDate = "X-Amz-Date"
	headerAMZHash = "X-Amz-Content-Sha256"
	headerMode    = "X-Amz-Object-Lock-Mode"
	headerUntil   = "X-Amz-Object-Lock-Retain-Until-Date"

	// maxObjectBytes bounds what one answer may hold. An object a stranger wrote must
	// never exhaust the reader's memory.
	maxObjectBytes = 64 << 20
)

// errTooLarge is an answer longer than maxObjectBytes.
var errTooLarge = errors.New("answer larger than the archive reads")

// ArchiveStoreConfig addresses a bucket with S3 Object Lock, on any provider that
// speaks the S3 API. The bucket is named in the path, not in the host name.
type ArchiveStoreConfig struct {
	Endpoint  string
	Region    string
	Bucket    string
	AccessKey string
	SecretKey string
}

// objectVersion is one stored version of an object, or a marker that hides it.
type objectVersion struct {
	Key            string
	VersionID      string
	IsDeleteMarker bool
	LastModified   time.Time
}

// lockedObject is an object's content with the lock it is stored under.
type lockedObject struct {
	Body        []byte
	Mode        string
	RetainUntil time.Time
}

// objectStore is what the archive needs from its bucket.
type objectStore interface {
	// LockEnabled reports whether the bucket has Object Lock.
	LockEnabled(ctx context.Context) (bool, error)
	// PutLocked stores a new object under a compliance lock and returns its version.
	// It reports false, and stores nothing, when the key already exists.
	PutLocked(ctx context.Context, key string, body []byte, retainUntil time.Time) (versionID string, created bool, err error)
	// Get reads one version.
	Get(ctx context.Context, key, versionID string) (*lockedObject, error)
	// Versions lists every version and delete marker under prefix.
	Versions(ctx context.Context, prefix string) ([]objectVersion, error)
}

type s3Store struct {
	cfg  ArchiveStoreConfig
	http *http.Client
	now  func() time.Time
}

func newS3Store(cfg ArchiveStoreConfig) *s3Store {
	cfg.Endpoint = strings.TrimSuffix(cfg.Endpoint, "/")
	// A redirect would send the request, signed for another host, somewhere else. The
	// body is hashed as stored, so the transport must not decompress it.
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DisableCompression = true
	client := &http.Client{
		Timeout:       10 * time.Minute,
		Transport:     transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return &s3Store{cfg: cfg, http: client, now: time.Now}
}

// s3Error is an answer the store gave, as opposed to not reaching it.
type s3Error struct {
	Status int
	Code   string `xml:"Code"`
}

func (e *s3Error) Error() string { return fmt.Sprintf("status %d %s", e.Status, e.Code) }

func (s *s3Store) LockEnabled(ctx context.Context) (bool, error) {
	body, _, err := s.do(ctx, http.MethodGet, "", url.Values{"object-lock": {""}}, nil, nil)
	var answered *s3Error
	if errors.As(err, &answered) && answered.Code == "ObjectLockConfigurationNotFoundError" {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("audit.s3Store.LockEnabled: %w", err)
	}
	var config struct {
		Enabled string `xml:"ObjectLockEnabled"`
	}
	if err := xml.Unmarshal(body, &config); err != nil {
		return false, fmt.Errorf("audit.s3Store.LockEnabled: decode: %w", err)
	}
	return config.Enabled == "Enabled", nil
}

func (s *s3Store) PutLocked(
	ctx context.Context, key string, body []byte, retainUntil time.Time,
) (versionID string, created bool, err error) {
	sum := md5.Sum(body) //nolint:gosec // see the import
	_, header, err := s.do(ctx, http.MethodPut, key, nil, http.Header{
		"Content-Md5":   {base64.StdEncoding.EncodeToString(sum[:])},
		"Content-Type":  {"application/gzip"},
		"If-None-Match": {"*"},
		headerMode:      {lockModeCompliance},
		headerUntil:     {retainUntil.UTC().Format(time.RFC3339)},
	}, body)
	var answered *s3Error
	if errors.As(err, &answered) && answered.Status == http.StatusPreconditionFailed {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("audit.s3Store.PutLocked: %w", err)
	}
	return header.Get("X-Amz-Version-Id"), true, nil
}

func (s *s3Store) Get(ctx context.Context, key, versionID string) (*lockedObject, error) {
	body, header, err := s.do(ctx, http.MethodGet, key, url.Values{"versionId": {versionID}}, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("audit.s3Store.Get: %w", err)
	}
	object := &lockedObject{Body: body, Mode: header.Get(headerMode)}
	if until := header.Get(headerUntil); until != "" {
		if object.RetainUntil, err = time.Parse(time.RFC3339, until); err != nil {
			return nil, fmt.Errorf("audit.s3Store.Get: retain-until date: %w", err)
		}
	}
	return object, nil
}

func (s *s3Store) Versions(ctx context.Context, prefix string) ([]objectVersion, error) {
	var out []objectVersion
	query := url.Values{"versions": {""}, "prefix": {prefix}}
	for {
		body, _, err := s.do(ctx, http.MethodGet, "", query, nil, nil)
		if err != nil {
			return nil, fmt.Errorf("audit.s3Store.Versions: %w", err)
		}
		// Versions and markers are interleaved and their order is the answer, so both
		// are read from one list of elements rather than from two.
		var page struct {
			IsTruncated         bool
			NextKeyMarker       string
			NextVersionIDMarker string `xml:"NextVersionIdMarker"`
			Entries             []struct {
				XMLName      xml.Name
				Key          string
				VersionID    string `xml:"VersionId"`
				LastModified time.Time
			} `xml:",any"`
		}
		if err := xml.Unmarshal(body, &page); err != nil {
			return nil, fmt.Errorf("audit.s3Store.Versions: decode: %w", err)
		}
		for _, entry := range page.Entries {
			switch entry.XMLName.Local {
			case "Version", "DeleteMarker":
				out = append(out, objectVersion{
					Key: entry.Key, VersionID: entry.VersionID, IsDeleteMarker: entry.XMLName.Local == "DeleteMarker",
					LastModified: entry.LastModified,
				})
			}
		}
		if !page.IsTruncated {
			return out, nil
		}
		if page.NextKeyMarker == "" {
			return nil, errors.New("audit.s3Store.Versions: truncated listing without a marker to continue from")
		}
		query.Set("key-marker", page.NextKeyMarker)
		query.Del("version-id-marker")
		if page.NextVersionIDMarker != "" {
			query.Set("version-id-marker", page.NextVersionIDMarker)
		}
	}
}

// do sends one signed request and returns the answer's body and headers. A status
// other than 2xx comes back as *s3Error. A conflict, a server error or no answer at
// all is tried once more: every call here may be repeated, a conditional upload too.
func (s *s3Store) do(
	ctx context.Context, method, key string, query url.Values, header http.Header, body []byte,
) ([]byte, http.Header, error) {
	data, answer, err := s.send(ctx, method, key, query, header, body)
	var answered *s3Error
	if err == nil || errors.Is(err, errTooLarge) ||
		(errors.As(err, &answered) && answered.Status != http.StatusConflict && answered.Status < 500) {
		return data, answer, err
	}
	select {
	case <-ctx.Done():
		return nil, nil, err
	case <-time.After(time.Second):
	}
	return s.send(ctx, method, key, query, header, body)
}

func (s *s3Store) send(
	ctx context.Context, method, key string, query url.Values, header http.Header, body []byte,
) ([]byte, http.Header, error) {
	path := "/" + s.cfg.Bucket
	if key != "" {
		path += "/" + key
	}
	u, err := url.Parse(s.cfg.Endpoint)
	if err != nil {
		return nil, nil, errors.New("the endpoint is not a valid URL")
	}
	u.Path, u.RawPath, u.RawQuery = path, escapeS3(path, true), canonicalQuery(query)

	req, err := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(body))
	if err != nil {
		return nil, nil, err
	}
	maps.Copy(req.Header, header)
	signS3(req, body, s.cfg, s.now())

	resp, err := s.http.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxObjectBytes+1))
	if err != nil {
		return nil, nil, err
	}
	if len(data) > maxObjectBytes {
		return nil, nil, errTooLarge
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		answered := &s3Error{}
		_ = xml.Unmarshal(data, answered)
		answered.Status = resp.StatusCode
		return nil, nil, answered
	}
	return data, resp.Header, nil
}

// signS3 adds an AWS Signature Version 4 to req, covering its method, path, query,
// host, body and every header it already carries.
func signS3(req *http.Request, body []byte, cfg ArchiveStoreConfig, now time.Time) {
	stamp := now.UTC().Format(amzDateFormat)
	day := stamp[:8]
	req.Header.Set(headerAMZDate, stamp)
	req.Header.Set(headerAMZHash, sha256Hex(body))

	headers := map[string]string{"host": req.URL.Host}
	for name, values := range req.Header {
		headers[strings.ToLower(name)] = strings.TrimSpace(strings.Join(values, ","))
	}
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	slices.Sort(names)
	var canonicalHeaders strings.Builder
	for _, name := range names {
		canonicalHeaders.WriteString(name + ":" + headers[name] + "\n")
	}
	signedHeaders := strings.Join(names, ";")

	canonical := strings.Join([]string{
		req.Method, req.URL.EscapedPath(), req.URL.RawQuery,
		canonicalHeaders.String(), signedHeaders, req.Header.Get(headerAMZHash),
	}, "\n")
	scope := day + "/" + cfg.Region + "/s3/aws4_request"
	toSign := strings.Join([]string{"AWS4-HMAC-SHA256", stamp, scope, sha256Hex([]byte(canonical))}, "\n")

	key := hmacSHA256([]byte("AWS4"+cfg.SecretKey), day)
	for _, part := range []string{cfg.Region, "s3", "aws4_request"} {
		key = hmacSHA256(key, part)
	}
	req.Header.Set("Authorization", fmt.Sprintf("AWS4-HMAC-SHA256 Credential=%s/%s,SignedHeaders=%s,Signature=%s",
		cfg.AccessKey, scope, signedHeaders, hex.EncodeToString(hmacSHA256(key, toSign))))
}

func hmacSHA256(key []byte, data string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(data))
	return mac.Sum(nil)
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// canonicalQuery sorts and encodes a query the way the signature expects, which is
// also what is sent: url.Values encodes a space as "+".
func canonicalQuery(query url.Values) string {
	var parts []string
	for name, values := range query {
		for _, value := range values {
			parts = append(parts, escapeS3(name, false)+"="+escapeS3(value, false))
		}
	}
	slices.Sort(parts)
	return strings.Join(parts, "&")
}

// escapeS3 percent-encodes everything outside the unreserved characters, and keeps
// "/" in a path.
func escapeS3(s string, keepSlash bool) string {
	const unreserved = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~"
	var out strings.Builder
	for _, b := range []byte(s) {
		if strings.IndexByte(unreserved, b) >= 0 || (keepSlash && b == '/') {
			out.WriteByte(b)
			continue
		}
		fmt.Fprintf(&out, "%%%02X", b)
	}
	return out.String()
}
