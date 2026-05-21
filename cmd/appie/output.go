package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

// ---- Exit codes (shared CLI contract) ----

const (
	exitOK            = 0
	exitUserError     = 1
	exitAuthError     = 2
	exitUpstreamError = 3
	exitNotFound      = 4
)

// ---- Sentinel errors ----

// Sentinel errors used for classification via errors.Is. Their Error()
// strings are written into user-facing `message` fields when chained with
// fmt.Errorf("...: %w: %w", err, sentinel), so phrase them like prose, not
// codes — the classifier reads the identity, not the string.
var (
	errAuth      = errors.New("not authenticated")
	errNotFound  = errors.New("not found")
	errAmbiguous = errors.New("ambiguous match")
	errBadArgs   = errors.New("bad arguments")
	errUpstream  = errors.New("upstream request failed")
)

// ---- Envelope ----

type errorPayload struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

type envelope struct {
	OK       bool           `json:"ok"`
	Data     any            `json:"data,omitempty"`
	Meta     map[string]any `json:"meta,omitempty"`
	Warnings []string       `json:"warnings,omitempty"`
	Error    *errorPayload  `json:"error,omitempty"`
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// ---- Warnings ----

// Warnings collects user-facing messages that should be surfaced under the
// "warnings" key in JSON mode and printed to stderr in text mode.
type Warnings struct {
	items []string
}

func (w *Warnings) Add(format string, args ...any) {
	if w == nil {
		return
	}
	w.items = append(w.items, fmt.Sprintf(format, args...))
}

func (w *Warnings) Slice() []string {
	if w == nil || len(w.items) == 0 {
		return nil
	}
	return w.items
}

// ---- Emit helpers ----

// emitJSON writes a success envelope to stdout. Only call when --json is set.
func emitJSON(data any, warnings []string) error {
	return writeJSON(os.Stdout, envelope{OK: true, Data: data, Warnings: warnings})
}

// emitJSONMeta is emitJSON with an additional `meta` block.
func emitJSONMeta(data any, meta map[string]any, warnings []string) error {
	return writeJSON(os.Stdout, envelope{OK: true, Data: data, Meta: meta, Warnings: warnings})
}

// emitError writes an error envelope to stdout. Returns the wrapped error so
// the caller can also propagate it. If stdout is unwritable (broken pipe,
// etc.) the error code and message are written to stderr so the failure is
// not entirely silent.
func emitError(err error, code string) error {
	writeErr := writeJSON(os.Stdout, envelope{
		OK:    false,
		Error: &errorPayload{Code: code, Message: err.Error()},
	})
	if writeErr != nil {
		fmt.Fprintf(os.Stderr, "appie: %s: %s\n", code, err.Error())
	}
	return err
}

func emitErrorDetails(err error, code string, details map[string]any) error {
	writeErr := writeJSON(os.Stdout, envelope{
		OK:    false,
		Error: &errorPayload{Code: code, Message: err.Error(), Details: details},
	})
	if writeErr != nil {
		fmt.Fprintf(os.Stderr, "appie: %s: %s\n", code, err.Error())
	}
	return err
}

// ambiguousError carries the candidate matches alongside the message so
// callers can render them under error.details in JSON mode.
type ambiguousError struct {
	msg        string
	candidates []map[string]any
}

func (e *ambiguousError) Error() string { return e.msg }
func (e *ambiguousError) Is(target error) bool {
	return target == errAmbiguous
}

func newAmbiguous(msg string, candidates []map[string]any) *ambiguousError {
	return &ambiguousError{msg: msg, candidates: candidates}
}

// errorCode classifies an error into (code string, exit code) using the
// sentinel errors above.
func errorCode(err error) (string, int) {
	switch {
	case err == nil:
		return "", exitOK
	case errors.Is(err, errAuth):
		return "not_authenticated", exitAuthError
	case errors.Is(err, errNotFound):
		return "not_found", exitNotFound
	case errors.Is(err, errAmbiguous):
		return "ambiguous", exitUserError
	case errors.Is(err, errBadArgs):
		return "bad_args", exitUserError
	case errors.Is(err, errUpstream):
		return "upstream_failed", exitUpstreamError
	default:
		return "upstream_failed", exitUpstreamError
	}
}

// ---- Mode-aware text/progress helpers ----

// warnf records a warning. In text mode it goes to stderr immediately; in JSON
// mode it accumulates in the supplied Warnings (callers must include
// w.Slice() in their emit call).
func warnf(w *Warnings, format string, args ...any) {
	if globalOpts.JSON {
		w.Add(format, args...)
		return
	}
	fmt.Fprintf(os.Stderr, "warning: "+format+"\n", args...)
}

// infof prints an informational message. Goes to stdout in text mode; stderr
// in JSON mode so the JSON envelope on stdout stays clean.
func infof(format string, args ...any) {
	if globalOpts.JSON {
		fmt.Fprintf(os.Stderr, format+"\n", args...)
		return
	}
	fmt.Printf(format+"\n", args...)
}

// progress prints a status line. Goes to stdout in text mode (preserving the
// pre-JSON UX); in JSON mode it's recorded as a warning so consumers can see
// it.
func progress(w *Warnings, format string, args ...any) {
	if globalOpts.JSON {
		w.Add(format, args...)
		return
	}
	fmt.Printf(format+"\n", args...)
}
