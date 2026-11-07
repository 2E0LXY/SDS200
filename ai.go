package main

// Natural-language control. The assistant is given a set of scanner tools and
// runs a short tool-calling loop: Google (Gemini) first, OpenRouter as the
// fallback when Google is unconfigured, rate-limited or failing.
//
// Both providers are called over plain HTTP with the standard library; no SDK
// is pulled in, so the single-binary build is unchanged.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Overridable in tests.
var (
	googleEndpointForTest     = "https://generativelanguage.googleapis.com/v1beta"
	openRouterEndpointForTest = "https://openrouter.ai/api/v1"
)

const (
	// Model ids change often. These are only defaults: both can be overridden
	// in config, and an unknown-model error triggers discovery of a working
	// one from the provider's own model list.
	defaultGoogleModel     = "gemini-flash-latest"
	defaultOpenRouterModel = "google/gemini-2.5-flash"
	defaultAIMaxSteps      = 8
)

// AIConfig is persisted in config.json. API keys are never returned by the API.
type AIConfig struct {
	Enabled          bool   `json:"enabled"`
	GoogleAPIKey     string `json:"google_api_key,omitempty"`
	GoogleModel      string `json:"google_model,omitempty"`
	OpenRouterAPIKey string `json:"openrouter_api_key,omitempty"`
	OpenRouterModel  string `json:"openrouter_model,omitempty"`
	MaxSteps         int    `json:"max_steps,omitempty"`
	TimeoutS         int    `json:"timeout_s,omitempty"`
}

func (c AIConfig) googleModel() string {
	if c.GoogleModel != "" {
		return c.GoogleModel
	}
	return defaultGoogleModel
}
func (c AIConfig) openRouterModel() string {
	if c.OpenRouterModel != "" {
		return c.OpenRouterModel
	}
	return defaultOpenRouterModel
}
func (c AIConfig) maxSteps() int {
	if c.MaxSteps > 0 && c.MaxSteps <= 24 {
		return c.MaxSteps
	}
	return defaultAIMaxSteps
}
func (c AIConfig) timeout() time.Duration {
	if c.TimeoutS > 0 && c.TimeoutS <= 300 {
		return time.Duration(c.TimeoutS) * time.Second
	}
	return 90 * time.Second
}

// ---------------------------------------------------------------- tool types

// AITool is one scanner operation the assistant may call.
type AITool struct {
	Name        string
	Description string
	// Parameters is a JSON-Schema object ({"type":"object","properties":{…}}).
	Parameters map[string]any
	// Writes marks a tool that changes the scanner; those need operator control.
	Writes bool
	Run    func(args map[string]any) (any, error)
}

// toolCall is one request from the model, provider-independent.
type toolCall struct {
	ID   string
	Name string
	Args map[string]any
	// Raw provider-specific data that must be echoed back in the next turn
	// (Gemini 3 rejects a function result whose call has no thought signature).
	Signature string
}

// AIStep records what actually happened, for the UI and the log.
type AIStep struct {
	Tool   string         `json:"tool"`
	Args   map[string]any `json:"args,omitempty"`
	Result any            `json:"result,omitempty"`
	Error  string         `json:"error,omitempty"`
}

// AIResult is the reply to one prompt.
type AIResult struct {
	Reply    string   `json:"reply"`
	Steps    []AIStep `json:"steps"`
	Provider string   `json:"provider"`
	Model    string   `json:"model"`
	Fallback string   `json:"fallback_reason,omitempty"`
}

// aiProvider is one LLM backend.
type aiProvider interface {
	name() string
	model() string
	// turn sends the conversation and returns either text or tool calls.
	turn(ctx context.Context, system string, msgs []aiMessage, tools []AITool) (text string, calls []toolCall, err error)
}

// aiMessage is the provider-independent conversation record.
type aiMessage struct {
	Role    string // "user", "assistant", "tool"
	Text    string
	Calls   []toolCall     // assistant turns that called tools
	CallID  string         // tool results
	ToolFor string         // tool name a result belongs to
	Result  map[string]any // tool result payload
}

// ---------------------------------------------------------------- the runner

type aiRunner struct {
	cfg    AIConfig
	tools  []AITool
	log    *AppLogger
	client *http.Client
}

func newAIRunner(cfg AIConfig, tools []AITool, log *AppLogger) *aiRunner {
	return &aiRunner{cfg: cfg, tools: tools, log: log, client: &http.Client{Timeout: 60 * time.Second}}
}

func (r *aiRunner) providers() []aiProvider {
	var ps []aiProvider
	if r.cfg.GoogleAPIKey != "" {
		ps = append(ps, &googleProvider{key: r.cfg.GoogleAPIKey, mdl: r.cfg.googleModel(), client: r.client, log: r.log})
	}
	if r.cfg.OpenRouterAPIKey != "" {
		ps = append(ps, &openRouterProvider{key: r.cfg.OpenRouterAPIKey, mdl: r.cfg.openRouterModel(), client: r.client, log: r.log})
	}
	return ps
}

func (r *aiRunner) toolByName(n string) *AITool {
	for i := range r.tools {
		if r.tools[i].Name == n {
			return &r.tools[i]
		}
	}
	return nil
}

var errNoProvider = errors.New("no AI provider configured: add a Google API key (or an OpenRouter key) in Settings")

// Run answers one prompt, executing scanner tools as the model asks for them.
func (r *aiRunner) Run(ctx context.Context, prompt string, system string) (*AIResult, error) {
	ps := r.providers()
	if len(ps) == 0 {
		return nil, errNoProvider
	}
	var lastErr error
	var fallback string
	for i, p := range ps {
		res, err := r.runWith(ctx, p, prompt, system)
		if err == nil {
			res.Fallback = fallback
			return res, nil
		}
		lastErr = err
		fallback = fmt.Sprintf("%s failed (%v)", p.name(), err)
		if i+1 < len(ps) {
			r.log.Warn("AI", "Provider failed, falling back", map[string]any{"provider": p.name(), "error": err.Error(), "next": ps[i+1].name()})
		}
	}
	return nil, lastErr
}

func (r *aiRunner) runWith(ctx context.Context, p aiProvider, prompt, system string) (*AIResult, error) {
	ctx, cancel := context.WithTimeout(ctx, r.cfg.timeout())
	defer cancel()
	msgs := []aiMessage{{Role: "user", Text: prompt}}
	out := &AIResult{Provider: p.name(), Model: p.model(), Steps: []AIStep{}}
	for step := 0; step < r.cfg.maxSteps(); step++ {
		text, calls, err := p.turn(ctx, system, msgs, r.tools)
		if err != nil {
			return nil, err
		}
		if len(calls) == 0 {
			out.Reply = strings.TrimSpace(text)
			if out.Reply == "" {
				out.Reply = "(no answer)"
			}
			return out, nil
		}
		msgs = append(msgs, aiMessage{Role: "assistant", Text: text, Calls: calls})
		for _, c := range calls {
			s := AIStep{Tool: c.Name, Args: c.Args}
			payload := map[string]any{}
			t := r.toolByName(c.Name)
			switch {
			case t == nil:
				s.Error = "unknown tool"
				payload["error"] = s.Error
			default:
				r.log.Info("AI", "Tool call", map[string]any{"tool": c.Name, "args": c.Args})
				v, err := t.Run(c.Args)
				if err != nil {
					s.Error = err.Error()
					payload["error"] = err.Error()
				} else {
					s.Result = v
					payload["result"] = v
				}
			}
			out.Steps = append(out.Steps, s)
			msgs = append(msgs, aiMessage{Role: "tool", CallID: c.ID, ToolFor: c.Name, Result: payload})
		}
	}
	out.Reply = "Stopped: the assistant used too many steps without finishing."
	return out, nil
}

// ------------------------------------------------------------ Google backend

type googleProvider struct {
	key    string
	mdl    string
	client *http.Client
	log    *AppLogger
	// resolved is a model id discovered after an unknown-model error.
	resolved string
}

func (g *googleProvider) name() string { return "google" }
func (g *googleProvider) model() string {
	if g.resolved != "" {
		return g.resolved
	}
	return g.mdl
}

type gPart struct {
	Text             string          `json:"text,omitempty"`
	FunctionCall     *gFunctionCall  `json:"functionCall,omitempty"`
	FunctionResponse *gFunctionReply `json:"functionResponse,omitempty"`
	ThoughtSignature string          `json:"thoughtSignature,omitempty"`
}
type gFunctionCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args,omitempty"`
}
type gFunctionReply struct {
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}
type gContent struct {
	Role  string  `json:"role,omitempty"`
	Parts []gPart `json:"parts"`
}

func (g *googleProvider) turn(ctx context.Context, system string, msgs []aiMessage, tools []AITool) (string, []toolCall, error) {
	decls := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		decls = append(decls, map[string]any{"name": t.Name, "description": t.Description, "parameters": t.Parameters})
	}
	body := map[string]any{
		"contents":          gContents(msgs),
		"tools":             []map[string]any{{"functionDeclarations": decls}},
		"systemInstruction": gContent{Parts: []gPart{{Text: system}}},
		"generationConfig":  map[string]any{"temperature": 0.2},
	}
	raw, err := g.post(ctx, g.model(), body)
	if err != nil {
		var ue unknownModelError
		if errors.As(err, &ue) {
			m, derr := g.discoverModel(ctx)
			if derr != nil {
				return "", nil, err
			}
			g.resolved = m
			g.log.Info("AI", "Google model resolved", map[string]any{"model": m})
			if raw, err = g.post(ctx, m, body); err != nil {
				return "", nil, err
			}
		} else {
			return "", nil, err
		}
	}
	var resp struct {
		Candidates []struct {
			Content      gContent `json:"content"`
			FinishReason string   `json:"finishReason"`
		} `json:"candidates"`
		PromptFeedback struct {
			BlockReason string `json:"blockReason"`
		} `json:"promptFeedback"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", nil, fmt.Errorf("google: bad JSON: %w", err)
	}
	if len(resp.Candidates) == 0 {
		if resp.PromptFeedback.BlockReason != "" {
			return "", nil, fmt.Errorf("google blocked the request (%s)", resp.PromptFeedback.BlockReason)
		}
		return "", nil, errors.New("google returned no candidates")
	}
	var text strings.Builder
	var calls []toolCall
	for _, p := range resp.Candidates[0].Content.Parts {
		if p.Text != "" {
			text.WriteString(p.Text)
		}
		if p.FunctionCall != nil {
			calls = append(calls, toolCall{ID: p.FunctionCall.Name, Name: p.FunctionCall.Name, Args: p.FunctionCall.Args, Signature: p.ThoughtSignature})
		}
	}
	return text.String(), calls, nil
}

// gContents converts the conversation. Gemini 3 requires the thought signature
// that came with a function call to be echoed back with it.
func gContents(msgs []aiMessage) []gContent {
	out := make([]gContent, 0, len(msgs))
	for _, m := range msgs {
		switch m.Role {
		case "user":
			out = append(out, gContent{Role: "user", Parts: []gPart{{Text: m.Text}}})
		case "assistant":
			parts := []gPart{}
			if m.Text != "" {
				parts = append(parts, gPart{Text: m.Text})
			}
			for _, c := range m.Calls {
				parts = append(parts, gPart{FunctionCall: &gFunctionCall{Name: c.Name, Args: c.Args}, ThoughtSignature: c.Signature})
			}
			out = append(out, gContent{Role: "model", Parts: parts})
		case "tool":
			out = append(out, gContent{Role: "user", Parts: []gPart{{FunctionResponse: &gFunctionReply{Name: m.ToolFor, Response: m.Result}}}})
		}
	}
	return out
}

type unknownModelError struct{ model string }

func (e unknownModelError) Error() string { return "unknown model " + e.model }

func (g *googleProvider) post(ctx context.Context, model string, body any) ([]byte, error) {
	b, _ := json.Marshal(body)
	url := fmt.Sprintf("%s/models/%s:generateContent", googleEndpointForTest, model)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", g.key)
	res, err := g.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("google: %w", err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if res.StatusCode == 404 || (res.StatusCode == 400 && bytes.Contains(bytes.ToLower(raw), []byte("not found"))) {
		return nil, unknownModelError{model}
	}
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("google HTTP %d: %s", res.StatusCode, shorten(strings.TrimSpace(string(raw)), 300))
	}
	return raw, nil
}

// discoverModel picks a current model that supports generateContent, so a
// renamed default does not break the feature.
func (g *googleProvider) discoverModel(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", googleEndpointForTest+"/models?pageSize=200", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("x-goog-api-key", g.key)
	res, err := g.client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	var list struct {
		Models []struct {
			Name                       string   `json:"name"`
			SupportedGenerationMethods []string `json:"supportedGenerationMethods"`
		} `json:"models"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(&list); err != nil {
		return "", err
	}
	var names []string
	for _, m := range list.Models {
		id := strings.TrimPrefix(m.Name, "models/")
		gen := false
		for _, s := range m.SupportedGenerationMethods {
			if s == "generateContent" {
				gen = true
			}
		}
		if !gen || !strings.HasPrefix(id, "gemini-") {
			continue
		}
		low := strings.ToLower(id)
		if strings.Contains(low, "image") || strings.Contains(low, "vision") || strings.Contains(low, "embedding") || strings.Contains(low, "tts") || strings.Contains(low, "live") {
			continue
		}
		names = append(names, id)
	}
	if len(names) == 0 {
		return "", errors.New("google: no usable model found")
	}
	// Prefer a flash model, then the highest version string.
	sort.Slice(names, func(i, j int) bool {
		fi, fj := strings.Contains(names[i], "flash"), strings.Contains(names[j], "flash")
		if fi != fj {
			return fi
		}
		return names[i] > names[j]
	})
	return names[0], nil
}

// -------------------------------------------------------- OpenRouter backend

type openRouterProvider struct {
	key    string
	mdl    string
	client *http.Client
	log    *AppLogger
}

func (o *openRouterProvider) name() string  { return "openrouter" }
func (o *openRouterProvider) model() string { return o.mdl }

type orToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

func (o *openRouterProvider) turn(ctx context.Context, system string, msgs []aiMessage, tools []AITool) (string, []toolCall, error) {
	chat := []map[string]any{{"role": "system", "content": system}}
	for _, m := range msgs {
		switch m.Role {
		case "user":
			chat = append(chat, map[string]any{"role": "user", "content": m.Text})
		case "assistant":
			msg := map[string]any{"role": "assistant", "content": m.Text}
			if len(m.Calls) > 0 {
				tc := make([]map[string]any, 0, len(m.Calls))
				for _, c := range m.Calls {
					args, _ := json.Marshal(c.Args)
					tc = append(tc, map[string]any{"id": c.ID, "type": "function", "function": map[string]any{"name": c.Name, "arguments": string(args)}})
				}
				msg["tool_calls"] = tc
			}
			chat = append(chat, msg)
		case "tool":
			b, _ := json.Marshal(m.Result)
			chat = append(chat, map[string]any{"role": "tool", "tool_call_id": m.CallID, "name": m.ToolFor, "content": string(b)})
		}
	}
	defs := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		defs = append(defs, map[string]any{"type": "function", "function": map[string]any{"name": t.Name, "description": t.Description, "parameters": t.Parameters}})
	}
	body := map[string]any{"model": o.mdl, "messages": chat, "tools": defs, "temperature": 0.2}
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", openRouterEndpointForTest+"/chat/completions", bytes.NewReader(b))
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+o.key)
	req.Header.Set("HTTP-Referer", "https://github.com/2E0LXY/SDS200")
	req.Header.Set("X-Title", "SDS200 WebApp")
	res, err := o.client.Do(req)
	if err != nil {
		return "", nil, fmt.Errorf("openrouter: %w", err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if res.StatusCode != 200 {
		return "", nil, fmt.Errorf("openrouter HTTP %d: %s", res.StatusCode, shorten(strings.TrimSpace(string(raw)), 300))
	}
	var resp struct {
		Choices []struct {
			Message struct {
				Content   string       `json:"content"`
				ToolCalls []orToolCall `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", nil, fmt.Errorf("openrouter: bad JSON: %w", err)
	}
	if len(resp.Choices) == 0 {
		if resp.Error.Message != "" {
			return "", nil, errors.New("openrouter: " + resp.Error.Message)
		}
		return "", nil, errors.New("openrouter returned no choices")
	}
	m := resp.Choices[0].Message
	var calls []toolCall
	for _, c := range m.ToolCalls {
		args := map[string]any{}
		if c.Function.Arguments != "" {
			_ = json.Unmarshal([]byte(c.Function.Arguments), &args)
		}
		id := c.ID
		if id == "" {
			id = c.Function.Name
		}
		calls = append(calls, toolCall{ID: id, Name: c.Function.Name, Args: args})
	}
	return m.Content, calls, nil
}

// ------------------------------------------------------------------ helpers

func argString(args map[string]any, key string) string {
	if v, ok := args[key]; ok {
		return strings.TrimSpace(fmt.Sprint(v))
	}
	return ""
}

func argInt(args map[string]any, key string) (int, bool) {
	v, ok := args[key]
	if !ok {
		return 0, false
	}
	switch x := v.(type) {
	case float64:
		return int(x), true
	case int:
		return x, true
	case string:
		var n int
		if _, err := fmt.Sscanf(strings.TrimSpace(x), "%d", &n); err == nil {
			return n, true
		}
	}
	return 0, false
}

func argFloat(args map[string]any, key string) (float64, bool) {
	v, ok := args[key]
	if !ok {
		return 0, false
	}
	switch x := v.(type) {
	case float64:
		return x, true
	case int:
		return float64(x), true
	case string:
		var f float64
		if _, err := fmt.Sscanf(strings.TrimSpace(x), "%g", &f); err == nil {
			return f, true
		}
	}
	return 0, false
}

func argBool(args map[string]any, key string) (bool, bool) {
	v, ok := args[key]
	if !ok {
		return false, false
	}
	switch x := v.(type) {
	case bool:
		return x, true
	case string:
		s := strings.ToLower(strings.TrimSpace(x))
		return s == "true" || s == "on" || s == "yes" || s == "1", true
	case float64:
		return x != 0, true
	}
	return false, false
}

func schema(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func strProp(desc string, enum ...string) map[string]any {
	p := map[string]any{"type": "string", "description": desc}
	if len(enum) > 0 {
		p["enum"] = enum
	}
	return p
}
func intProp(desc string, min, max int) map[string]any {
	return map[string]any{"type": "integer", "description": desc, "minimum": min, "maximum": max}
}
func numProp(desc string) map[string]any {
	return map[string]any{"type": "number", "description": desc}
}
func boolProp(desc string) map[string]any {
	return map[string]any{"type": "boolean", "description": desc}
}
