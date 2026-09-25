package txbuild

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Abdullah1738/juno-sdk-go/junoscan"
)

const scannerHTTPTimeout = 15 * time.Second

type scannerHealthReader interface {
	Health(context.Context) (junoscan.HealthResponse, error)
}

// scannerHealthClient reads juno-scan /v1/health for the anchor checks.
//
// The planner only needs status, scanned_height and scanned_hash. Newer
// scanners also report event_epoch, which the SDK client requires, but
// juno-scan v1.4.x never sends it and txbuild does not read wallet events, so
// it is ignored here.
type scannerHealthClient struct {
	baseURL    string
	httpClient *http.Client
}

func newScannerHealthClient(baseURL string, httpClient *http.Client) (*scannerHealthClient, error) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return nil, errors.New("junoscan: base url required")
	}
	u, err := url.Parse(baseURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("junoscan: invalid base url %q", baseURL)
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: scannerHTTPTimeout}
	}
	return &scannerHealthClient{baseURL: strings.TrimRight(baseURL, "/"), httpClient: httpClient}, nil
}

func (c *scannerHealthClient) Health(ctx context.Context) (junoscan.HealthResponse, error) {
	if c == nil {
		return junoscan.HealthResponse{}, errors.New("txbuild: scanner is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/health", nil)
	if err != nil {
		return junoscan.HealthResponse{}, errors.New("junoscan: build request")
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return junoscan.HealthResponse{}, err
	}
	defer resp.Body.Close()

	const maxBody = 1 << 20
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return junoscan.HealthResponse{}, fmt.Errorf("junoscan: read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return junoscan.HealthResponse{}, scannerHTTPError(resp, raw)
	}

	var body struct {
		Status        string  `json:"status"`
		ScannedHeight *int64  `json:"scanned_height"`
		ScannedHash   *string `json:"scanned_hash"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return junoscan.HealthResponse{}, errors.New("junoscan: invalid json response")
	}
	return junoscan.HealthResponse{
		Status:        body.Status,
		ScannedHeight: body.ScannedHeight,
		ScannedHash:   body.ScannedHash,
	}, nil
}

func scannerHTTPError(resp *http.Response, raw []byte) *junoscan.HTTPError {
	out := &junoscan.HTTPError{
		StatusCode: resp.StatusCode,
		Status:     resp.Status,
		Body:       string(raw),
	}
	type errorBody struct {
		Code      string          `json:"code"`
		Message   string          `json:"message"`
		Retryable bool            `json:"retryable"`
		Details   json.RawMessage `json:"details"`
	}
	var envelope struct {
		Error errorBody `json:"error"`
		errorBody
	}
	if json.Unmarshal(raw, &envelope) != nil {
		return out
	}
	body := envelope.errorBody
	if strings.TrimSpace(envelope.Error.Code) != "" || strings.TrimSpace(envelope.Error.Message) != "" {
		body = envelope.Error
	}
	out.Code = strings.TrimSpace(body.Code)
	out.Message = strings.TrimSpace(body.Message)
	out.Retryable = body.Retryable
	out.Details = body.Details
	return out
}
