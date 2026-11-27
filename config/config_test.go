package config_test

import (
	"strings"
	"testing"
	"time"

	"github.com/darmoise/go-kit/config"
)

func env(m map[string]string) *config.Env {
	return config.New(func(k string) string { return m[k] })
}

func TestDefaults(t *testing.T) {
	t.Parallel()
	e := env(map[string]string{"EMPTY": ""})
	if e.String("EMPTY", "d") != "d" || e.String("NONE", "d") != "d" {
		t.Fatal("String default")
	}
	if e.Int("NONE", 99999, 1, 10) != 99999 { // def outside the range is not checked
		t.Fatal("Int default")
	}
	if e.Duration("NONE", time.Hour, 0, time.Second) != time.Hour {
		t.Fatal("Duration default")
	}
	if !e.Bool("NONE", true) {
		t.Fatal("Bool default")
	}
	if e.Enum("NONE", "x", "a", "b") != "x" {
		t.Fatal("Enum default")
	}
	if e.List("NONE") != nil {
		t.Fatal("List default")
	}
	if e.Err() != nil {
		t.Fatalf("unexpected err: %v", e.Err())
	}
}

func TestValues(t *testing.T) {
	t.Parallel()
	e := env(map[string]string{
		"S": "  as is ", "I": " 8081 ", "D": " 1m30s ", "B": " true ", "E": " b ", "L": " a, b ,, c ",
	})
	if e.String("S", "") != "  as is " {
		t.Fatal("String must not trim")
	}
	if e.Int("I", 1, 1, 65535) != 8081 {
		t.Fatal("Int")
	}
	if e.Duration("D", 0, 0, time.Hour) != 90*time.Second {
		t.Fatal("Duration")
	}
	if !e.Bool("B", false) {
		t.Fatal("Bool")
	}
	if e.Enum("E", "a", "a", "b") != "b" {
		t.Fatal("Enum")
	}
	if got := e.List("L"); strings.Join(got, "|") != "a|b|c" {
		t.Fatalf("List = %q", got)
	}
	if e.Err() != nil {
		t.Fatal(e.Err())
	}
}

func TestListEdge(t *testing.T) {
	t.Parallel()
	if got := env(map[string]string{"L": "a,,b"}).List("L"); strings.Join(got, "|") != "a|b" {
		t.Fatalf("a,,b = %q", got)
	}
}

func TestIntBounds(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		v    string
		want int
		ok   bool
	}{{"5", 5, true}, {"10", 10, true}, {"4", 7, false}, {"11", 7, false}, {"abc", 7, false}, {"1.5", 7, false}} {
		e := env(map[string]string{"N": tc.v})
		got := e.Int("N", 7, 5, 10)
		if got != tc.want || (e.Err() == nil) != tc.ok {
			t.Errorf("Int %q: got %d err %v", tc.v, got, e.Err())
		}
	}
}

func TestDurationBounds(t *testing.T) {
	t.Parallel()
	min, max := time.Second, time.Minute
	def := 5 * time.Second
	for _, tc := range []struct {
		v    string
		want time.Duration
		ok   bool
	}{{"1s", time.Second, true}, {"1m", time.Minute, true}, {"999ms", def, false}, {"61s", def, false}, {"bad", def, false}} {
		e := env(map[string]string{"D": tc.v})
		got := e.Duration("D", def, min, max)
		if got != tc.want || (e.Err() == nil) != tc.ok {
			t.Errorf("Duration %q: got %v err %v", tc.v, got, e.Err())
		}
	}
}

func TestBoolEnumErrors(t *testing.T) {
	t.Parallel()
	e := env(map[string]string{"B": "maybe", "E": "c"})
	if !e.Bool("B", true) {
		t.Fatal("Bool must return def on error")
	}
	if e.Enum("E", "a", "a", "b") != "a" {
		t.Fatal("Enum must return def on error")
	}
	msg := e.Err().Error()
	if !strings.Contains(msg, "B: must be boolean") || !strings.Contains(msg, "E: must be one of a, b") {
		t.Fatalf("err = %q", msg)
	}
}

func TestRequired(t *testing.T) {
	t.Parallel()
	e := env(map[string]string{"OK": "v"})
	if e.Required("OK") != "v" || e.Err() != nil {
		t.Fatal("Required ok")
	}
	if e.Required("MISSING") != "" || e.Err() == nil || e.Err().Error() != "MISSING: required" {
		t.Fatalf("err = %v", e.Err())
	}
}

func TestFailfAndErrJoin(t *testing.T) {
	t.Parallel()
	e := env(map[string]string{"A": "x", "B": "y"})
	e.Int("A", 1, 1, 2)
	e.Failf("CROSS", "must be less than %d", 5)
	e.Bool("B", false)
	e.Required("C")
	lines := strings.Split(e.Err().Error(), "\n")
	want := []string{"A: must be integer in [1, 2]", "CROSS: must be less than 5", "B: must be boolean", "C: required"}
	if strings.Join(lines, "|") != strings.Join(want, "|") {
		t.Fatalf("lines = %q", lines)
	}
}

func TestSecretNotInErrors(t *testing.T) {
	t.Parallel()
	const secret = "topsecret-value-123"
	e := env(map[string]string{"S1": secret, "S2": secret, "S3": secret, "S4": secret})
	e.Int("S1", 0, 0, 1)
	e.Duration("S2", 0, 0, time.Second)
	e.Bool("S3", false)
	e.Enum("S4", "a", "a", "b")
	if err := e.Err(); err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("err = %v", err)
	}
	// A value outside the range does not leak into the text either.
	e2 := env(map[string]string{"N": "123456789", "D": "999h"})
	e2.Int("N", 1, 1, 10)
	e2.Duration("D", time.Second, 0, time.Minute)
	if s := e2.Err().Error(); strings.Contains(s, "123456789") || strings.Contains(s, "999h") {
		t.Fatalf("value leaked: %s", s)
	}
}

// Not parallel: t.Setenv.
func TestNewNilUsesOSEnv(t *testing.T) {
	t.Setenv("GOKIT_CONFIG_TEST_VAR", "from-os")
	if got := config.New(nil).String("GOKIT_CONFIG_TEST_VAR", ""); got != "from-os" {
		t.Fatalf("got %q", got)
	}
}
