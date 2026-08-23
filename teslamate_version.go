package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	defaultTeslaMateWebURL = "http://teslamate:4000"
	teslaMateVersionTTL    = 5 * time.Minute
	maxTeslaMateHTMLBytes  = 2 << 20
)

var (
	teslaMateVersionPattern = regexp.MustCompile(`(?is)<th[^>]*>\s*(?:Version|版本)\s*</th>\s*<td[^>]*>\s*(?:<[^>]+>\s*)*([0-9]+\.[0-9]+\.[0-9]+)`)
	teslaMateAboutPattern   = regexp.MustCompile(`(?is)<table[^>]*class=["'][^"']*\babout\b[^"']*["'][^>]*>.*?<td[^>]*>.*?([0-9]+\.[0-9]+\.[0-9]+)`)
	semanticVersionPattern  = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
	teslaMateVersionClient  = &http.Client{
		Timeout: 3 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	teslaMateVersionClock = time.Now
	teslaMateVersionState struct {
		sync.Mutex
		version   string
		source    string
		checkedAt time.Time
	}
)

// currentTeslaMateVersion prefers the live TeslaMate settings page on the
// private Docker network. Install metadata remains a bounded fallback only;
// it is never allowed to mask a newer live version after a TeslaMate upgrade.
func currentTeslaMateVersion(ctx context.Context) (version, source string, checkedAt time.Time) {
	now := teslaMateVersionClock().UTC()
	teslaMateVersionState.Lock()
	if !teslaMateVersionState.checkedAt.IsZero() && now.Sub(teslaMateVersionState.checkedAt) < teslaMateVersionTTL {
		version = teslaMateVersionState.version
		source = teslaMateVersionState.source
		checkedAt = teslaMateVersionState.checkedAt
		teslaMateVersionState.Unlock()
		return
	}
	teslaMateVersionState.Unlock()

	version, err := fetchLiveTeslaMateVersion(ctx)
	if err == nil {
		source = "live_web"
	} else {
		version = normalizeTeslaMateVersion(os.Getenv("TESLAMATE_VERSION"))
		if version != "" {
			source = "install_metadata"
		}
	}

	teslaMateVersionState.Lock()
	teslaMateVersionState.version = version
	teslaMateVersionState.source = source
	teslaMateVersionState.checkedAt = now
	teslaMateVersionState.Unlock()
	return version, source, now
}

func fetchLiveTeslaMateVersion(ctx context.Context) (string, error) {
	settingsURL, err := teslaMateSettingsURL(os.Getenv("TESLAMATE_WEB_URL"))
	if err != nil {
		return "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, settingsURL.String(), nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("Accept", "text/html,application/xhtml+xml")
	request.Header.Set("User-Agent", "My-T-Companion/"+apiVersion)

	response, err := teslaMateVersionClient.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("TeslaMate settings returned HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxTeslaMateHTMLBytes+1))
	if err != nil {
		return "", err
	}
	if len(body) > maxTeslaMateHTMLBytes {
		return "", errors.New("TeslaMate settings response is too large")
	}
	match := teslaMateVersionPattern.FindSubmatch(body)
	if len(match) != 2 {
		// TeslaMate localizes the row label. The stable `table.about` selector
		// keeps the probe independent of the UI language.
		match = teslaMateAboutPattern.FindSubmatch(body)
	}
	if len(match) != 2 {
		return "", errors.New("TeslaMate version was not present in the settings page")
	}
	version := normalizeTeslaMateVersion(string(match[1]))
	if version == "" {
		return "", errors.New("TeslaMate returned an invalid version")
	}
	return version, nil
}

func teslaMateSettingsURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = defaultTeslaMateWebURL
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, errors.New("TESLAMATE_WEB_URL must be an absolute HTTP(S) URL")
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	path := strings.TrimRight(parsed.Path, "/")
	if !strings.HasSuffix(path, "/settings") {
		path += "/settings"
	}
	parsed.Path = path
	return parsed, nil
}

func normalizeTeslaMateVersion(raw string) string {
	value := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "v"))
	if !semanticVersionPattern.MatchString(value) {
		return ""
	}
	return value
}
