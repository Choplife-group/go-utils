package env

import (
	"strings"
	"testing"
	"time"
)

func TestStringFallsBackOnBlank(t *testing.T) {

	t.Setenv("DATABASE_HOST", "   ")

	if got := String("DATABASE_HOST", "localhost"); got != "localhost" {
		t.Errorf("String() = %q, want the default", got)
	}

}

func TestIntFallsBackOnUnparseable(t *testing.T) {

	t.Setenv("DATABASE_MAX_CONNECTION", "not-a-number")

	if got := Int("DATABASE_MAX_CONNECTION", 10); got != 10 {
		t.Errorf("Int() = %d, want the default", got)
	}

	t.Setenv("DATABASE_MAX_CONNECTION", "25")

	if got := Int("DATABASE_MAX_CONNECTION", 10); got != 25 {
		t.Errorf("Int() = %d, want 25", got)
	}
}

func TestBool(t *testing.T) {

	t.Setenv("REDIS_TRACING", "yes")

	if !Bool("REDIS_TRACING", false) {
		t.Error("Bool() = false, want true")
	}

	t.Setenv("REDIS_TRACING", "off")

	if Bool("REDIS_TRACING", true) {
		t.Error("Bool() = true, want false")
	}

	t.Setenv("REDIS_TRACING", "nonsense")

	if !Bool("REDIS_TRACING", true) {
		t.Error("Bool() = false, want the default")
	}
}

func TestSecondsRejectsNonPositive(t *testing.T) {

	t.Setenv("DATABASE_CONNECTION_LIFETIME", "0")

	if got := Seconds("DATABASE_CONNECTION_LIFETIME", time.Minute); got != time.Minute {
		t.Errorf("Seconds() = %s, want the default", got)
	}

	t.Setenv("DATABASE_CONNECTION_LIFETIME", "90")

	if got := Seconds("DATABASE_CONNECTION_LIFETIME", time.Minute); got != 90*time.Second {
		t.Errorf("Seconds() = %s, want 90s", got)
	}
}

// TestRedactNeverLeaksPassword is the test that matters: every connection string
// this package handles ends up in an error message somewhere, and 28 services
// currently log these with the password in the clear.
func TestRedactNeverLeaksPassword(t *testing.T) {

	const password = "sup3rs3cr3t"

	cases := []string{
		"amqp://admin:" + password + "@rabbit:5672/chop",
		"redis://default:" + password + "@cache:6379",
		"admin:" + password + "@tcp(mysql:3306)/identity?charset=utf8",
		"amqp://admin:" + password + "@rabbit:5672/",
	}

	for _, raw := range cases {

		got := Redact(raw)

		if strings.Contains(got, password) {
			t.Errorf("Redact(%q) leaked the password: %q", raw, got)
		}

		if got == "" {
			t.Errorf("Redact(%q) returned an empty string", raw)
		}
	}
}

func TestRedactLeavesCredentiallessStringsAlone(t *testing.T) {

	cases := []string{
		"",
		"amqp://rabbit:5672/",
		"tcp(mysql:3306)/identity",
	}

	for _, raw := range cases {

		if got := Redact(raw); got != raw {
			t.Errorf("Redact(%q) = %q, want it unchanged", raw, got)
		}
	}
}

func TestRedactMasksUnparseableInput(t *testing.T) {

	got := Redact("://:@ not a uri")

	if strings.Contains(got, "not a uri") {
		t.Errorf("Redact() passed through unparseable input: %q", got)
	}
}
