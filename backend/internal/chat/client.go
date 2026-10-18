// Package chat provides bounded dashboard authoring over an OpenAI-compatible API.
package chat

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/nightnoryu/tesseract/internal/platform/telemetry"
)

var ErrUnavailable = errors.New("chat_unavailable")
var ErrInvalid = errors.New("invalid_chat")
var ErrBusy = errors.New("chat_busy")

type Config struct {
	Endpoint, Token, Model string
	InsecureSkipTLSVerify  bool
}
type Client struct {
	config  Config
	http    *http.Client
	metrics *telemetry.Metrics
}
type Function struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}
type Call struct {
	ID       string   `json:"id"`
	Type     string   `json:"type"`
	Function Function `json:"function"`
}
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	Calls   []Call `json:"tool_calls,omitempty"`
	CallID  string `json:"tool_call_id,omitempty"`
}
type Tool struct {
	Type     string         `json:"type"`
	Function ToolDefinition `json:"function"`
}
type ToolDefinition struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  any    `json:"parameters"`
}

func NewClient(c Config, metrics *telemetry.Metrics) (*Client, error) {
	if c.Endpoint == "" && c.Token == "" && c.Model == "" && !c.InsecureSkipTLSVerify {
		return nil, nil
	}
	u, err := url.Parse(c.Endpoint)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http") || strings.TrimSpace(c.Model) == "" || len(c.Model) > 200 || strings.TrimSpace(c.Token) == "" || strings.ContainsAny(c.Token, "\r\n") {
		return nil, errors.New("invalid_llm_config")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: c.InsecureSkipTLSVerify} // Explicit operator opt-in, isolated to this client.
	return &Client{c, &http.Client{Transport: transport, Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, metrics}, nil
}
func (c *Client) Complete(ctx context.Context, messages []Message, tools []Tool) (out Message, err error) {
	start := time.Now()
	defer func() {
		if c.metrics != nil {
			c.metrics.ObserveLLM(time.Since(start), err != nil)
		}
	}()
	body, err := json.Marshal(struct {
		Model     string    `json:"model"`
		Messages  []Message `json:"messages"`
		Tools     []Tool    `json:"tools"`
		MaxTokens int       `json:"max_tokens"`
	}{c.config.Model, messages, tools, 4096})
	if err != nil || len(body) > 256<<10 {
		return out, ErrInvalid
	}
	req, err := http.NewRequestWithContext(ctx, "POST", c.config.Endpoint, bytes.NewReader(body))
	if err != nil {
		return out, ErrUnavailable
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.config.Token)
	resp, err := c.http.Do(req)
	if err != nil {
		return out, ErrUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return out, ErrUnavailable
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (128<<10)+1))
	if err != nil || len(raw) > 128<<10 {
		return out, ErrUnavailable
	}
	var result struct {
		Choices []struct {
			Message Message `json:"message"`
			Finish  string  `json:"finish_reason"`
		} `json:"choices"`
	}
	if json.Unmarshal(raw, &result) != nil || len(result.Choices) != 1 {
		return out, ErrUnavailable
	}
	choice := result.Choices[0]
	out = choice.Message
	if out.Role != "assistant" || (choice.Finish != "stop" && choice.Finish != "tool_calls") || len(out.Calls) > 8 || len(out.Content) > 16000 {
		return Message{}, ErrUnavailable
	}
	return out, nil
}
