package auth

import (
	"context"
	"net/http"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/registry"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
)

type streamCanceledChunkExecutor struct {
	id string
}

func (e *streamCanceledChunkExecutor) Identifier() string {
	return e.id
}

func (e *streamCanceledChunkExecutor) Execute(context.Context, *Auth, cliproxyexecutor.Request, cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	return cliproxyexecutor.Response{}, &Error{Code: "not_implemented", Message: "Execute not implemented"}
}

func (e *streamCanceledChunkExecutor) ExecuteStream(context.Context, *Auth, cliproxyexecutor.Request, cliproxyexecutor.Options) (*cliproxyexecutor.StreamResult, error) {
	ch := make(chan cliproxyexecutor.StreamChunk, 1)
	ch <- cliproxyexecutor.StreamChunk{Err: context.Canceled}
	close(ch)
	return &cliproxyexecutor.StreamResult{Chunks: ch}, nil
}

func (e *streamCanceledChunkExecutor) Refresh(_ context.Context, auth *Auth) (*Auth, error) {
	return auth, nil
}

func (e *streamCanceledChunkExecutor) CountTokens(context.Context, *Auth, cliproxyexecutor.Request, cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	return cliproxyexecutor.Response{}, &Error{Code: "not_implemented", Message: "CountTokens not implemented"}
}

func (e *streamCanceledChunkExecutor) HttpRequest(context.Context, *Auth, *http.Request) (*http.Response, error) {
	return nil, nil
}

type streamCanceledStartExecutor struct {
	id string
}

func (e *streamCanceledStartExecutor) Identifier() string {
	return e.id
}

func (e *streamCanceledStartExecutor) Execute(context.Context, *Auth, cliproxyexecutor.Request, cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	return cliproxyexecutor.Response{}, &Error{Code: "not_implemented", Message: "Execute not implemented"}
}

func (e *streamCanceledStartExecutor) ExecuteStream(context.Context, *Auth, cliproxyexecutor.Request, cliproxyexecutor.Options) (*cliproxyexecutor.StreamResult, error) {
	return nil, context.Canceled
}

func (e *streamCanceledStartExecutor) Refresh(_ context.Context, auth *Auth) (*Auth, error) {
	return auth, nil
}

func (e *streamCanceledStartExecutor) CountTokens(context.Context, *Auth, cliproxyexecutor.Request, cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	return cliproxyexecutor.Response{}, &Error{Code: "not_implemented", Message: "CountTokens not implemented"}
}

func (e *streamCanceledStartExecutor) HttpRequest(context.Context, *Auth, *http.Request) (*http.Response, error) {
	return nil, nil
}

func TestManager_ExecuteStream_ContextCanceledChunk_DoesNotMarkFailure(t *testing.T) {
	m := NewManager(nil, nil, nil)
	m.RegisterExecutor(&streamCanceledChunkExecutor{id: "claude"})

	auth := &Auth{
		ID:       "auth-stream-cancel-chunk",
		Provider: "claude",
		Status:   StatusActive,
	}
	if _, err := m.Register(context.Background(), auth); err != nil {
		t.Fatalf("register auth: %v", err)
	}

	registry.GetGlobalRegistry().RegisterClient(auth.ID, auth.Provider, []*registry.ModelInfo{{ID: "test-model"}})
	t.Cleanup(func() {
		registry.GetGlobalRegistry().UnregisterClient(auth.ID)
	})

	stream, err := m.ExecuteStream(context.Background(), []string{"claude"}, cliproxyexecutor.Request{
		Model: "test-model",
	}, cliproxyexecutor.Options{})
	if err != nil {
		t.Fatalf("ExecuteStream returned error: %v", err)
	}
	if stream == nil {
		t.Fatalf("ExecuteStream returned nil stream")
	}

	var gotCanceled bool
	for chunk := range stream.Chunks {
		if chunk.Err == context.Canceled {
			gotCanceled = true
		}
	}
	if !gotCanceled {
		t.Fatalf("expected a context canceled chunk error")
	}

	updated, ok := m.GetByID(auth.ID)
	if !ok || updated == nil {
		t.Fatalf("expected auth to exist")
	}
	if updated.Status != StatusActive {
		t.Fatalf("status = %q, want %q", updated.Status, StatusActive)
	}
	if updated.LastError != nil {
		t.Fatalf("expected LastError to remain nil, got %+v", updated.LastError)
	}
	if updated.Unavailable {
		t.Fatalf("expected auth to remain available")
	}
	if !updated.NextRetryAfter.IsZero() {
		t.Fatalf("expected NextRetryAfter to remain zero, got %v", updated.NextRetryAfter)
	}
	if state, exists := updated.ModelStates["test-model"]; exists && state != nil {
		t.Fatalf("expected no model state mutation for canceled stream chunk, got %+v", state)
	}
}

func TestManager_ExecuteStream_ContextCanceledAtStart_DoesNotMarkFailure(t *testing.T) {
	m := NewManager(nil, nil, nil)
	m.RegisterExecutor(&streamCanceledStartExecutor{id: "claude"})

	auth := &Auth{
		ID:       "auth-stream-cancel-start",
		Provider: "claude",
		Status:   StatusActive,
	}
	if _, err := m.Register(context.Background(), auth); err != nil {
		t.Fatalf("register auth: %v", err)
	}

	registry.GetGlobalRegistry().RegisterClient(auth.ID, auth.Provider, []*registry.ModelInfo{{ID: "test-model"}})
	t.Cleanup(func() {
		registry.GetGlobalRegistry().UnregisterClient(auth.ID)
	})

	_, err := m.ExecuteStream(context.Background(), []string{"claude"}, cliproxyexecutor.Request{
		Model: "test-model",
	}, cliproxyexecutor.Options{})
	if err != context.Canceled {
		t.Fatalf("expected context.Canceled, got %v", err)
	}

	updated, ok := m.GetByID(auth.ID)
	if !ok || updated == nil {
		t.Fatalf("expected auth to exist")
	}
	if updated.Status != StatusActive {
		t.Fatalf("status = %q, want %q", updated.Status, StatusActive)
	}
	if updated.LastError != nil {
		t.Fatalf("expected LastError to remain nil, got %+v", updated.LastError)
	}
	if updated.Unavailable {
		t.Fatalf("expected auth to remain available")
	}
	if !updated.NextRetryAfter.IsZero() {
		t.Fatalf("expected NextRetryAfter to remain zero, got %v", updated.NextRetryAfter)
	}
	if state, exists := updated.ModelStates["test-model"]; exists && state != nil {
		t.Fatalf("expected no model state mutation for startup cancellation, got %+v", state)
	}
}
