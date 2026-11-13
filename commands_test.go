package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"phi-agent-code/internal/api"
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
	origSelect := selectModel
	origOnSet := onModelSet

	prov = p
	selectModel = nil
	onModelSet = nil

	t.Cleanup(func() {
		prov = origProv
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

