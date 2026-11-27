// Package config reads configuration from environment variables and accumulates
// errors: the application reads all variables in a row and at the end gets one error
// listing all problems.
//
// A variable's value never appears in error text (it may be a secret). Env is not
// safe for concurrent use: the config is read from a single goroutine at startup.
package config

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Env reads variables through lookup and accumulates errors.
type Env struct {
	lookup func(string) string
	errs   []error
}

// New creates an Env. lookup == nil means os.Getenv.
func New(lookup func(string) string) *Env {
	if lookup == nil {
		lookup = os.Getenv
	}
	return &Env{lookup: lookup}
}

func (e *Env) fail(name, format string, args ...any) {
	e.errs = append(e.errs, fmt.Errorf("%s: %s", name, fmt.Sprintf(format, args...)))
}

// String returns the value as is, or def if the variable is empty or unset.
func (e *Env) String(name, def string) string {
	if v := e.lookup(name); v != "" {
		return v
	}
	return def
}

// Required returns the value; an empty value is a "required" error.
func (e *Env) Required(name string) string {
	v := e.lookup(name)
	if v == "" {
		e.fail(name, "required")
	}
	return v
}

// Int reads an integer from the range [min, max]. For an empty value it returns
// def without checking; on error it returns def and accumulates the error.
func (e *Env) Int(name string, def, min, max int) int {
	raw := strings.TrimSpace(e.lookup(name))
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < min || n > max {
		e.fail(name, "must be integer in [%d, %d]", min, max)
		return def
	}
	return n
}

// Duration reads a duration (time.ParseDuration) from the range [min, max].
func (e *Env) Duration(name string, def, min, max time.Duration) time.Duration {
	raw := strings.TrimSpace(e.lookup(name))
	if raw == "" {
		return def
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		e.fail(name, "invalid duration")
		return def
	}
	if d < min || d > max {
		e.fail(name, "duration must be in [%s, %s]", min, max)
		return def
	}
	return d
}

// Bool reads a boolean value (strconv.ParseBool).
func (e *Env) Bool(name string, def bool) bool {
	raw := strings.TrimSpace(e.lookup(name))
	if raw == "" {
		return def
	}
	b, err := strconv.ParseBool(raw)
	if err != nil {
		e.fail(name, "must be boolean")
		return def
	}
	return b
}

// Enum reads a value that must exactly match one of allowed.
func (e *Env) Enum(name, def string, allowed ...string) string {
	raw := strings.TrimSpace(e.lookup(name))
	if raw == "" {
		return def
	}
	if !slices.Contains(allowed, raw) {
		e.fail(name, "must be one of %s", strings.Join(allowed, ", "))
		return def
	}
	return raw
}

// List splits the value by commas, trims spaces and drops empty
// elements. An unset variable yields nil.
func (e *Env) List(name string) []string {
	raw := strings.TrimSpace(e.lookup(name))
	if raw == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(raw, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Failf adds an error "NAME: <text>" for cross-checks. Secret values must not
// be formatted into the text.
func (e *Env) Failf(name, format string, args ...any) {
	e.fail(name, format, args...)
}

// Err returns all accumulated errors via errors.Join in call order
// or nil.
func (e *Env) Err() error {
	return errors.Join(e.errs...)
}
