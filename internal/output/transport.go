package output

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

func newHTTPClient(cfg Config) (*http.Client, error) {
	u, err := url.Parse(cfg.URL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Fragment != "" {
		return nil, fmt.Errorf("%s output requires an http(s) URL without embedded credentials or fragment", cfg.Type)
	}
	if cfg.RetryMax < 0 || cfg.RetryMax > 8 || cfg.RetryBackoffMS < 0 || cfg.RetryBackoffMS > 10000 {
		return nil, fmt.Errorf("retry_max must be 0..8 and retry_backoff_ms 0..10000")
	}
	if cfg.BatchSize > 10000 || cfg.FlushSec > 3600 {
		return nil, fmt.Errorf("batch_size must not exceed 10000 and flush_sec 3600")
	}
	for _, name := range []string{cfg.PasswordEnv, cfg.BearerTokenEnv} {
		if name != "" && strings.TrimSpace(os.Getenv(name)) == "" {
			return nil, fmt.Errorf("output credential environment variable %s is empty", name)
		}
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = 32
	transport.MaxIdleConnsPerHost = 8
	transport.MaxConnsPerHost = 16
	transport.IdleConnTimeout = 90 * time.Second
	if cfg.CAFile != "" {
		pem, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("read output CA: %w", err)
		}
		pool, err := x509.SystemCertPool()
		if err != nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("output ca_file contains no certificates")
		}
		transport.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	}
	return &http.Client{Timeout: 20 * time.Second, Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, nil
}

// post retries only transient failures; request bodies must be replayable.
// A remote acknowledgement lost in transport can produce a duplicate on retry.
func post(client *http.Client, cfg Config, request func() (*http.Request, error)) error {
	retries := cfg.RetryMax
	if retries <= 0 {
		retries = 2
	}
	backoff := time.Duration(cfg.RetryBackoffMS) * time.Millisecond
	if backoff <= 0 {
		backoff = 200 * time.Millisecond
	}
	var last error
	for attempt := 0; attempt <= retries; attempt++ {
		req, err := request()
		if err != nil {
			return err
		}
		if cfg.Username != "" {
			req.SetBasicAuth(cfg.Username, os.Getenv(cfg.PasswordEnv))
		}
		if cfg.BearerTokenEnv != "" {
			req.Header.Set("Authorization", "Bearer "+os.Getenv(cfg.BearerTokenEnv))
		}
		resp, err := client.Do(req)
		if err == nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 65536))
			_ = resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return nil
			}
			err = fmt.Errorf("HTTP %d", resp.StatusCode)
			if resp.StatusCode != 408 && resp.StatusCode != 429 && resp.StatusCode < 500 {
				return err
			}
		}
		last = err
		if attempt < retries {
			delay := backoff * time.Duration(1<<attempt)
			if delay > 5*time.Second {
				delay = 5 * time.Second
			}
			time.Sleep(delay)
		}
	}
	return last
}
