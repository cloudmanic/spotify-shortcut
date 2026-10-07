//
// Date: 2026-10-07
// Author: Spicer Matthews <spicer@cloudmanic.com>
// Copyright (c) 2026 Cloudmanic Labs, LLC. All rights reserved.
//
// Description: Small client for TypeSafe's Jev decision model. Jev takes a
// piece of text plus a set of typed questions (choice or yes/no) and returns
// structured answers with probabilities. /api/v1/ask uses it to turn a spoken
// request into an action.
//

package spotify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"time"
)

const (
	// DefaultJevModel is pinned to a version because the confidence
	// thresholds in ask.go were tuned against it; an alias like jev-latest
	// could move under us and shift those numbers.
	DefaultJevModel = "jev-1.13.0"

	// jevEndpoint is TypeSafe's single evaluation endpoint.
	jevEndpoint = "https://api.typesafe.ai/v1/systemone"

	// jevMaxAttempts covers TypeSafe's 429 (rate limit) and 529 (overloaded)
	// responses, which the API docs say to retry after a short delay.
	jevMaxAttempts = 3
)

// jevClient is the package-level Jev client used by Ask. Nil until main
// configures it from TYPESAFE_API_KEY.
var jevClient JevClient

// SetJevClient sets the Jev client used by /api/v1/ask.
func SetJevClient(c JevClient) {
	jevClient = c
}

// JevClient asks Jev a batch of questions about one piece of text. It is an
// interface so tests can return canned answers without calling TypeSafe.
type JevClient interface {
	Ask(ctx context.Context, state string, questions map[string]JevQuestion) (map[string]JevAnswer, error)
}

// JevOption is one option of a choice question. An empty Description is
// sent as null, which tells Jev the key speaks for itself.
type JevOption struct {
	Key         string
	Description string
}

// JevOptions is an ordered list of choice options. Jev leans toward options
// listed first, so we keep our own order instead of letting a Go map sort
// the keys alphabetically.
type JevOptions []JevOption

// MarshalJSON writes the options as a JSON object in slice order, which is
// the shape the TypeSafe API expects for choice criteria.
func (o JevOptions) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, opt := range o {
		if i > 0 {
			buf.WriteByte(',')
		}
		key, err := json.Marshal(opt.Key)
		if err != nil {
			return nil, err
		}
		buf.Write(key)
		buf.WriteByte(':')
		if opt.Description == "" {
			buf.WriteString("null")
			continue
		}
		desc, err := json.Marshal(opt.Description)
		if err != nil {
			return nil, err
		}
		buf.Write(desc)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// JevQuestion is one typed question. Options is set for choice questions
// and left empty for yes/no ("noul") questions.
type JevQuestion struct {
	Type         string     `json:"type"`
	Instructions string     `json:"instructions"`
	Options      JevOptions `json:"criteria,omitempty"`
}

// JevChoice builds a question that picks exactly one of the options.
func JevChoice(instructions string, options JevOptions) JevQuestion {
	return JevQuestion{Type: "choice", Instructions: instructions, Options: options}
}

// JevYesNo builds a question whose answer is the probability of "yes".
func JevYesNo(instructions string) JevQuestion {
	return JevQuestion{Type: "noul", Instructions: instructions}
}

// JevAnswer is Jev's answer to one question. Choice answers fill Choice,
// Probabilities and Confidence; yes/no answers fill Noul.
type JevAnswer struct {
	Type          string             `json:"type"`
	Choice        string             `json:"choice"`
	Probabilities map[string]float64 `json:"probabilities"`
	Confidence    float64            `json:"confidence"`
	Noul          float64            `json:"noul"`
}

// Top returns the most likely option and its probability.
func (a JevAnswer) Top() (string, float64) {
	return a.Choice, a.Probabilities[a.Choice]
}

// RunnerUp returns the second most likely option and its probability, so
// callers can tell a clear winner from a close call between two options.
func (a JevAnswer) RunnerUp() (string, float64) {
	keys := make([]string, 0, len(a.Probabilities))
	for k := range a.Probabilities {
		if k != a.Choice {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return "", 0
	}
	// Sort by probability, then by key so ties are deterministic.
	sort.Slice(keys, func(i, j int) bool {
		pi, pj := a.Probabilities[keys[i]], a.Probabilities[keys[j]]
		if pi != pj {
			return pi > pj
		}
		return keys[i] < keys[j]
	})
	return keys[0], a.Probabilities[keys[0]]
}

// typesafeClient is the HTTP implementation of JevClient.
type typesafeClient struct {
	apiKey   string
	model    string
	endpoint string
	http     *http.Client
}

// NewJevClient returns a JevClient that calls the TypeSafe API with the given
// key. An empty model falls back to DefaultJevModel.
func NewJevClient(apiKey, model string) JevClient {
	if model == "" {
		model = DefaultJevModel
	}
	return &typesafeClient{
		apiKey:   apiKey,
		model:    model,
		endpoint: jevEndpoint,
		http:     &http.Client{Timeout: 10 * time.Second},
	}
}

// jevRequest is the request body for the evaluation endpoint.
type jevRequest struct {
	State     string                 `json:"state"`
	Model     string                 `json:"model"`
	Questions map[string]JevQuestion `json:"questions"`
}

// jevResponse is the response body from the evaluation endpoint.
type jevResponse struct {
	Model   string               `json:"model"`
	Answers map[string]JevAnswer `json:"answers"`
}

// Ask sends every question in one request. Jev evaluates them in parallel,
// so one call with ten questions costs about the same time as one question.
func (c *typesafeClient) Ask(ctx context.Context, state string, questions map[string]JevQuestion) (map[string]JevAnswer, error) {
	body, err := json.Marshal(jevRequest{State: state, Model: c.model, Questions: questions})
	if err != nil {
		return nil, fmt.Errorf("encode jev request: %w", err)
	}

	var lastErr error
	for attempt := 1; attempt <= jevMaxAttempts; attempt++ {
		answers, retryAfter, err := c.post(ctx, body)
		if err == nil {
			return answers, nil
		}
		lastErr = err
		if retryAfter == 0 || attempt == jevMaxAttempts {
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(retryAfter):
		}
	}
	return nil, lastErr
}

// post makes one HTTP attempt. A non-zero duration means the error is
// retryable and says how long to wait first.
func (c *typesafeClient) post(ctx context.Context, body []byte) (map[string]JevAnswer, time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, 0, fmt.Errorf("build jev request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("jev request: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, fmt.Errorf("read jev response: %w", err)
	}

	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == 529 {
		return nil, retryDelay(resp.Header.Get("Retry-After")), fmt.Errorf("jev busy (HTTP %d)", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("jev HTTP %d: %s", resp.StatusCode, truncate(string(raw), 300))
	}

	var parsed jevResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, 0, fmt.Errorf("decode jev response: %w", err)
	}
	return parsed.Answers, 0, nil
}

// retryDelay honors a short Retry-After header and otherwise waits 300ms.
// Long waits are capped because a person is waiting on a spoken reply.
func retryDelay(header string) time.Duration {
	if secs, err := strconv.ParseFloat(header, 64); err == nil && secs > 0 {
		d := time.Duration(secs * float64(time.Second))
		if d > 2*time.Second {
			d = 2 * time.Second
		}
		return d
	}
	return 300 * time.Millisecond
}

// truncate shortens s to at most n bytes for log and error messages.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
