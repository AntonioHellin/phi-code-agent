package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"phi-agent-code/internal/agent"
	"phi-agent-code/internal/api"
	"phi-agent-code/internal/compact"
	"phi-agent-code/internal/provider"
)

func captureCommandStdout(t *testing.T, fn func()) string {
	t.Helper()
	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	os.Stdout = w

	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()

	fn()
	_ = w.Close()
	os.Stdout = origStdout
	out := <-done
	_ = r.Close()
	return out
}

func setupCommandGlobals(t *testing.T, p provider.Provider) {
	t.Helper()
	origProv := prov
	origAgent := rootAgent
	origCompactor := compactor
	origSelect := selectModel
	origOnSet := onModelSet

	prov = p
	rootAgent = nil
	compactor = compact.NoCompaction{}
	selectModel = nil
	onModelSet = nil

	t.Cleanup(func() {
		prov = origProv
		rootAgent = origAgent
		compactor = origCompactor
		selectModel = origSelect
		onModelSet = origOnSet
	})
}

type stubNonListerProvider struct {
	model string
}

func (s *stubNonListerProvider) Send(_ context.Context, _ []api.Message, _ []api.ToolDef) (api.Response, error) {
	return api.Response{}, nil
}
func (s *stubNonListerProvider) Model() string        { return s.model }
func (s *stubNonListerProvider) SetModel(name string) { s.model = name }

func TestCmdModel(t *testing.T) {
	t.Run("no args prints active model only", func(t *testing.T) {
		mock := &provider.MockProvider{
			AvailableModels: []string{"mock-a", "mock-b"},
		}
		mock.SetModel("mock-initial")
		setupCommandGlobals(t, mock)

		out := captureCommandStdout(t, func() {
			cmdModel("")
		})
		if !strings.Contains(out, "current: mock-initial") {
			t.Errorf("expected current model output, got %q", out)
		}
		if strings.Contains(out, "mock-a") || strings.Contains(out, "mock-b") {
			t.Errorf("expected /model with no args not to dump available models, got %q", out)
		}
	})

	t.Run("valid switch updates provider and banner", func(t *testing.T) {
		mock := &provider.MockProvider{
			AvailableModels: []string{"mock-a", "mock-b"},
		}
		mock.SetModel("mock-a")
		setupCommandGlobals(t, mock)

		var bannerUpdated string
		onModelSet = func(model string) {
			bannerUpdated = model
		}

		out := captureCommandStdout(t, func() {
			cmdModel("mock-b")
		})
		if mock.Model() != "mock-b" || bannerUpdated != "mock-b" {
			t.Errorf("expected model switch to 'mock-b', got prov=%q banner=%q", mock.Model(), bannerUpdated)
		}
		if !strings.Contains(out, "model: mock-b") {
			t.Errorf("expected confirmation line, got %q", out)
		}
	})

	t.Run("rejects unknown and cross-provider models", func(t *testing.T) {
		mock := &provider.MockProvider{
			AvailableModels: []string{"mock-a", "mock-b"},
		}
		mock.SetModel("mock-a")
		setupCommandGlobals(t, mock)

		bannerUpdated := ""
		onModelSet = func(model string) {
			bannerUpdated = model
		}

		unknownOut := captureCommandStdout(t, func() {
			cmdModel("nonexistent-model")
		})
		if mock.Model() != "mock-a" || bannerUpdated != "" {
			t.Fatalf("expected unknown model to be rejected without mutating state, got prov=%q banner=%q", mock.Model(), bannerUpdated)
		}
		if !strings.Contains(unknownOut, "error: unknown model") {
			t.Errorf("expected error message for unknown model, got %q", unknownOut)
		}

		crossOut := captureCommandStdout(t, func() {
			cmdModel("gemini-4-hydrogen")
		})
		if mock.Model() != "mock-a" || bannerUpdated != "" {
			t.Fatalf("expected cross-provider model to be rejected, got prov=%q banner=%q", mock.Model(), bannerUpdated)
		}
		if !strings.Contains(crossOut, "belongs to provider \"gemini\"") {
			t.Errorf("expected cross-provider diagnostic, got %q", crossOut)
		}
	})

	t.Run("offline fallback validates against curated knownModels", func(t *testing.T) {
		// Nil client forces ListModels error without network I/O, exercising curated fallback validation.
		gem := provider.NewGeminiProvider(nil, "gemini-4-hydrogen", 0, "")
		setupCommandGlobals(t, gem)

		okOut := captureCommandStdout(t, func() {
			cmdModel("gemini-4-carbon")
		})
		if gem.Model() != "gemini-4-carbon" || !strings.Contains(okOut, "model: gemini-4-carbon") {
			t.Errorf("expected curated gemini fallback model to succeed, got model=%q out=%q", gem.Model(), okOut)
		}

		badOut := captureCommandStdout(t, func() {
			cmdModel("not-a-real-model")
		})
		if gem.Model() != "gemini-4-carbon" || !strings.Contains(badOut, "error: unknown model") {
			t.Errorf("expected invalid model to be rejected under fallback, got model=%q out=%q", gem.Model(), badOut)
		}
	})

	t.Run("non-lister unknown provider rejects arbitrary models", func(t *testing.T) {
		stub := &stubNonListerProvider{model: "initial"}
		setupCommandGlobals(t, stub)

		out := captureCommandStdout(t, func() {
			cmdModel("patata")
		})
		if stub.Model() != "initial" || !strings.Contains(out, "not listed in any provider catalog") {
			t.Errorf("expected rejection for non-lister provider, got model=%q out=%q", stub.Model(), out)
		}
	})
}

func TestCmdModelsInteractiveAndFallback(t *testing.T) {
	t.Run("interactive selection applies model and banner callback", func(t *testing.T) {
		mock := &provider.MockProvider{
			AvailableModels: []string{"m1", "m2", "m3"},
		}
		mock.SetModel("m1")
		setupCommandGlobals(t, mock)

		var gotOptions []string
		var gotCurrent string
		var bannerUpdated string
		selectModel = func(_ string, options []string, current string) (string, bool) {
			gotOptions = options
			gotCurrent = current
			return "m2", true
		}
		onModelSet = func(model string) {
			bannerUpdated = model
		}

		out := captureCommandStdout(t, func() {
			cmdModels("")
		})
		if len(gotOptions) != 3 || gotCurrent != "m1" {
			t.Errorf("unexpected picker inputs: options=%v current=%q", gotOptions, gotCurrent)
		}
		if mock.Model() != "m2" || bannerUpdated != "m2" {
			t.Errorf("expected selected model 'm2', got prov=%q banner=%q", mock.Model(), bannerUpdated)
		}
		if !strings.Contains(out, "model: m2") {
			t.Errorf("expected switched confirmation, got %q", out)
		}
	})

	t.Run("canceled selection keeps active model", func(t *testing.T) {
		mock := &provider.MockProvider{
			AvailableModels: []string{"m1", "m2"},
		}
		mock.SetModel("m1")
		setupCommandGlobals(t, mock)

		selectModel = func(_ string, _ []string, _ string) (string, bool) {
			return "", false
		}

		out := captureCommandStdout(t, func() {
			cmdModels("")
		})
		if mock.Model() != "m1" {
			t.Errorf("expected active model 'm1' after cancel, got %q", mock.Model())
		}
		if !strings.Contains(out, "model selection canceled") {
			t.Errorf("expected cancellation message, got %q", out)
		}
	})

	t.Run("headless fallback prints available models when no UI selector", func(t *testing.T) {
		mock := &provider.MockProvider{
			AvailableModels: []string{"m1", "m2", "m3"},
		}
		setupCommandGlobals(t, mock)

		out := captureCommandStdout(t, func() {
			cmdModels("")
		})
		if !strings.Contains(out, "available models (3):") || !strings.Contains(out, "m3") {
			t.Errorf("expected fallback list output, got %q", out)
		}
	})
}

func TestFormatIntWithCommas(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		{0, "0"},
		{820, "820"},
		{12450, "12,450"},
		{1000000, "1,000,000"},
		{-1234, "-1,234"},
	}
	for _, tc := range cases {
		if got := formatIntWithCommas(tc.in); got != tc.want {
			t.Errorf("formatIntWithCommas(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestCmdTokens(t *testing.T) {
	t.Run("displays formatted token breakdown, budget, and EUR cost for known model", func(t *testing.T) {
		mock := &provider.MockProvider{
			Responses: []api.Response{
				{
					Content:    []api.Block{{Type: api.BlockText, Text: "ok"}},
					StopReason: "end_turn",
					Usage:      api.Usage{InputTokens: 12450, OutputTokens: 820, CacheReadTokens: 8100},
				},
			},
		}
		mock.SetModel("claude-sonnet-4-5-20250929")
		setupCommandGlobals(t, mock)

		compactor = &compact.WithLogging{
			Inner:   &compact.TokenBudget{MaxTokens: 40000},
			LogPath: "",
		}
		rootAgent = agent.New(agent.Agent{
			Name:      "root",
			Provider:  mock,
			Compactor: compactor,
			Verbose:   false,
		})
		if _, err := rootAgent.Run(context.Background(), "hello"); err != nil {
			t.Fatalf("unexpected Run error: %v", err)
		}

		out := captureCommandStdout(t, func() {
			cmdTokens("")
		})
		for _, want := range []string{
			"Session Token Usage:",
			"12,450",
			"820",
			"8,100",
			"21,370",
			"40,000 tokens",
			"€0.0479 EUR (claude-sonnet-4-5-20250929)",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("expected /tokens output to contain %q, got:\n%s", want, out)
			}
		}
	})

	t.Run("handles unknown rate and nil globals safely", func(t *testing.T) {
		mock := &provider.MockProvider{}
		mock.SetModel("custom-unknown-model")
		setupCommandGlobals(t, mock)

		out := captureCommandStdout(t, func() {
			cmdTokens("")
		})
		if !strings.Contains(out, `unknown rate for "custom-unknown-model"`) {
			t.Errorf("expected unknown rate notice, got:\n%s", out)
		}

		prov = nil
		nilOut := captureCommandStdout(t, func() {
			cmdTokens("")
		})
		if !strings.Contains(nilOut, "Session Token Usage:") {
			t.Errorf("expected safe zeroed output when globals are nil, got:\n%s", nilOut)
		}
	})
}

// TestFormatUsageStatus verifies live TUI usage status formatting across nil guards,
// zero initial usage, known EUR tiers, and unknown model fallback.
func TestFormatUsageStatus(t *testing.T) {
	mock := &provider.MockProvider{
		Responses: []api.Response{
			{
				Content:    []api.Block{{Type: api.BlockText, Text: "ok"}},
				StopReason: "end_turn",
				Usage:      api.Usage{InputTokens: 5884, OutputTokens: 47},
			},
		},
	}
	mock.SetModel("claude-sonnet-4-5-20250929")
	setupCommandGlobals(t, mock)

	// Before any turn is billed, the status line stays empty rather than showing zeros.
	if got := formatUsageStatus(); got != "" {
		t.Fatalf("expected empty status before first billed turn, got %q", got)
	}

	rootAgent = agent.New(agent.Agent{
		Name:     "root",
		Provider: mock,
		Verbose:  false,
	})
	if _, err := rootAgent.Run(context.Background(), "hello"); err != nil {
		t.Fatalf("unexpected Run error: %v", err)
	}

	wantKnown := "5,884 in · 47 out · ~€0.0169"
	if got := formatUsageStatus(); got != wantKnown {
		t.Errorf("formatUsageStatus() = %q, want %q", got, wantKnown)
	}

	// Switching to an unpriced model omits the EUR estimate instead of displaying €0.0000.
	mock.SetModel("custom-unknown-model")
	wantUnknown := "5,884 in · 47 out"
	if got := formatUsageStatus(); got != wantUnknown {
		t.Errorf("formatUsageStatus() with unknown model = %q, want %q", got, wantUnknown)
	}

	// Nil globals must return "" safely without panicking.
	rootAgent = nil
	if got := formatUsageStatus(); got != "" {
		t.Errorf("expected empty status when rootAgent is nil, got %q", got)
	}
	prov = nil
	if got := formatUsageStatus(); got != "" {
		t.Errorf("expected empty status when prov is nil, got %q", got)
	}
}

