package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
)

// captureStdout swaps os.Stdout for a pipe and returns whatever was written.
// Closes both pipe ends and restores stdout even if fn panics, so the copy
// goroutine cannot deadlock the test.
func captureStdout(t *testing.T, fn func()) []byte {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w

	done := make(chan []byte, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.Bytes()
	}()

	defer func() {
		os.Stdout = old
		_ = r.Close()
	}()

	func() {
		defer func() { _ = w.Close() }()
		fn()
	}()

	return <-done
}

func captureStderr(t *testing.T, fn func()) []byte {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = w

	done := make(chan []byte, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.Bytes()
	}()

	defer func() {
		os.Stderr = old
		_ = r.Close()
	}()

	func() {
		defer func() { _ = w.Close() }()
		fn()
	}()

	return <-done
}

func TestEmitJSONShape(t *testing.T) {
	out := captureStdout(t, func() {
		_ = emitJSON(map[string]any{"foo": "bar"}, nil)
	})

	var env map[string]any
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("unmarshal: %v\nraw: %s", err, out)
	}
	if env["ok"] != true {
		t.Errorf("ok = %v, want true", env["ok"])
	}
	if _, ok := env["warnings"]; ok {
		t.Errorf("warnings should be omitted when empty")
	}
	if _, ok := env["error"]; ok {
		t.Errorf("error should be omitted on success")
	}
}

func TestEmitErrorShape(t *testing.T) {
	out := captureStdout(t, func() {
		_ = emitError(errors.New("boom"), "bad_args")
	})

	var env map[string]any
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("unmarshal: %v\nraw: %s", err, out)
	}
	if env["ok"] != false {
		t.Errorf("ok = %v, want false", env["ok"])
	}
	errMap, ok := env["error"].(map[string]any)
	if !ok {
		t.Fatalf("error wrong type: %v", env["error"])
	}
	if errMap["code"] != "bad_args" {
		t.Errorf("code = %v", errMap["code"])
	}
	if errMap["message"] != "boom" {
		t.Errorf("message = %v", errMap["message"])
	}
}

func TestErrorCodeClassification(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		wantCode string
		wantExit int
	}{
		{"auth", fmt.Errorf("nope: %w", errAuth), "not_authenticated", exitAuthError},
		{"not_found", fmt.Errorf("nope: %w", errNotFound), "not_found", exitNotFound},
		{"ambiguous", fmt.Errorf("nope: %w", errAmbiguous), "ambiguous", exitUserError},
		{"bad_args", fmt.Errorf("nope: %w", errBadArgs), "bad_args", exitUserError},
		{"upstream", fmt.Errorf("nope: %w", errUpstream), "upstream_failed", exitUpstreamError},
		{"plain", errors.New("plain"), "upstream_failed", exitUpstreamError},
		{"nil", nil, "", exitOK},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, exit := errorCode(c.err)
			if code != c.wantCode || exit != c.wantExit {
				t.Errorf("got (%q,%d), want (%q,%d)", code, exit, c.wantCode, c.wantExit)
			}
		})
	}
}

func TestAmbiguousErrorClassifies(t *testing.T) {
	amb := newAmbiguous("multiple matches", []map[string]any{
		{"id": 1, "title": "A"},
		{"id": 2, "title": "B"},
	})

	if !errors.Is(amb, errAmbiguous) {
		t.Errorf("errors.Is(amb, errAmbiguous) = false; want true")
	}
	code, exit := errorCode(amb)
	if code != "ambiguous" || exit != exitUserError {
		t.Errorf("got (%q,%d), want (ambiguous, %d)", code, exit, exitUserError)
	}

	var got *ambiguousError
	if !errors.As(amb, &got) {
		t.Fatalf("errors.As did not match")
	}
	if len(got.candidates) != 2 {
		t.Errorf("candidates len = %d, want 2", len(got.candidates))
	}
}

func TestProgressRouting_TextMode(t *testing.T) {
	old := globalOpts.JSON
	globalOpts.JSON = false
	defer func() { globalOpts.JSON = old }()

	w := &Warnings{}
	out := captureStdout(t, func() { progress(w, "found: %s", "milk") })
	if !strings.Contains(string(out), "found: milk") {
		t.Errorf("text mode should print progress to stdout; got: %s", out)
	}
	if len(w.Slice()) != 0 {
		t.Errorf("text mode should not record warnings; got: %v", w.Slice())
	}
}

func TestProgressRouting_JSONMode(t *testing.T) {
	old := globalOpts.JSON
	globalOpts.JSON = true
	defer func() { globalOpts.JSON = old }()

	w := &Warnings{}
	out := captureStdout(t, func() { progress(w, "found: %s", "milk") })
	if len(out) != 0 {
		t.Errorf("JSON mode should not print progress to stdout; got: %s", out)
	}
	if got := w.Slice(); len(got) != 1 || got[0] != "found: milk" {
		t.Errorf("JSON mode should record warning; got: %v", got)
	}
}

func TestWarnfRouting_TextMode(t *testing.T) {
	old := globalOpts.JSON
	globalOpts.JSON = false
	defer func() { globalOpts.JSON = old }()

	w := &Warnings{}
	errOut := captureStderr(t, func() { warnf(w, "bad: %d", 42) })
	if !strings.Contains(string(errOut), "bad: 42") {
		t.Errorf("text mode should print warning to stderr; got: %s", errOut)
	}
	if len(w.Slice()) != 0 {
		t.Errorf("text mode should not accumulate; got: %v", w.Slice())
	}
}

func TestWarnfRouting_JSONMode(t *testing.T) {
	old := globalOpts.JSON
	globalOpts.JSON = true
	defer func() { globalOpts.JSON = old }()

	w := &Warnings{}
	errOut := captureStderr(t, func() { warnf(w, "bad: %d", 42) })
	if len(errOut) != 0 {
		t.Errorf("JSON mode should not write to stderr; got: %s", errOut)
	}
	if got := w.Slice(); len(got) != 1 || got[0] != "bad: 42" {
		t.Errorf("JSON mode should record; got: %v", got)
	}
}

func TestInfofRouting_JSONMode(t *testing.T) {
	old := globalOpts.JSON
	globalOpts.JSON = true
	defer func() { globalOpts.JSON = old }()

	out := captureStdout(t, func() {
		_ = captureStderr(t, func() { infof("hello %s", "world") })
	})
	if len(out) != 0 {
		t.Errorf("JSON mode should not write infof to stdout; got: %s", out)
	}
}
