package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	BaseURL, Token string
	HTTP           *http.Client
}
type APIError struct {
	Code   string
	Status int
}

func (e *APIError) Error() string { return "control API: " + e.Code }
func NewClient(base, token string) (*Client, error) {
	parsed, err := url.Parse(base)
	if err != nil || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("invalid control URL")
	}
	ip := net.ParseIP(parsed.Hostname())
	if ip == nil || !ip.IsLoopback() || parsed.Scheme != "http" {
		return nil, errors.New("control URL must use an explicit loopback HTTP address")
	}
	if token == "" {
		return nil, errors.New("internal token is required")
	}
	return &Client{BaseURL: strings.TrimRight(base, "/"), Token: token, HTTP: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (c *Client) Post(ctx context.Context, path string, body, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", c.BaseURL+"/internal/v1"+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.Token)
	res, err := c.HTTP.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return &APIError{Code: "CONTROL_UNAVAILABLE"}
	}
	defer res.Body.Close()
	var envelope struct {
		Data  json.RawMessage `json:"data"`
		Error *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 16<<20)).Decode(&envelope); err != nil {
		return &APIError{Code: "INVALID_RESPONSE", Status: res.StatusCode}
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 || envelope.Error != nil {
		code := "CONTROL_ERROR"
		if envelope.Error != nil {
			code = envelope.Error.Code
		}
		return &APIError{code, res.StatusCode}
	}
	if len(envelope.Data) == 0 {
		return &APIError{Code: "INVALID_RESPONSE", Status: res.StatusCode}
	}
	if out != nil {
		return json.Unmarshal(envelope.Data, out)
	}
	return nil
}
