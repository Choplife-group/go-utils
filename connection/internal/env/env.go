// Package env reads connection settings from the environment.
//
// Each reader takes the variable's full name, so every variable a connection
// package depends on is greppable at its call site. There is deliberately no
// prefix parameter: the platform has a fixed, small set of roles — a service's
// own database and the reports database, the service-local cache and the shared
// global one — and each gets its own named constructor spelling out the
// variables it reads.
package env

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// String returns the value of the named variable, or def when it is unset or
// blank.
func String(name, def string) string {

	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return def
	}

	return value
}

// Int returns the value of the named variable parsed as an integer, or def when
// it is unset or unparseable. The existing services all fall back silently on a
// bad value rather than failing, and that behaviour is preserved here.
func Int(name string, def int) int {

	value, err := strconv.Atoi(String(name, ""))
	if err != nil {
		return def
	}

	return value
}

// Bool reports whether the named variable is set to a true-ish value ("1",
// "true", "yes", "on"), returning def when it is unset.
func Bool(name string, def bool) bool {

	switch strings.ToLower(String(name, "")) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}

	return def
}

// Seconds returns the value of the named variable interpreted as a whole number
// of seconds, or def when it is unset or unparseable. The platform's existing
// variables (DATABASE_CONNECTION_LIFETIME, for one) are plain integers counted
// in seconds rather than Go duration strings.
func Seconds(name string, def time.Duration) time.Duration {

	value, err := strconv.Atoi(String(name, ""))
	if err != nil || value <= 0 {
		return def
	}

	return time.Duration(value) * time.Second
}

const redacted = "xxxxx"

// Redact replaces the password in a connection string so it can be put in a log
// line or an error message. It handles both the URI form used by RabbitMQ and
// Redis (amqp://user:pass@host/vhost) and the MySQL DSN form (user:pass@tcp(...)).
// A string it cannot parse is reported as "<redacted>" rather than returned as
// is, so a malformed value can never leak a password by falling through.
func Redact(raw string) string {

	if raw == "" {
		return ""
	}

	if strings.Contains(raw, "://") {
		return redactURI(raw)
	}

	return redactDSN(raw)
}

// redactURI masks the password of a standard scheme://user:pass@host URI.
func redactURI(raw string) string {

	parsed, err := url.Parse(raw)
	if err != nil || parsed.User == nil {

		if err != nil {
			return "<redacted>"
		}

		return raw
	}

	if _, hasPassword := parsed.User.Password(); !hasPassword {
		return raw
	}

	parsed.User = url.UserPassword(parsed.User.Username(), redacted)

	return parsed.String()
}

// redactDSN masks the password of a MySQL DSN, whose credentials sit before the
// last "@" and are separated from the username by the first ":".
func redactDSN(raw string) string {

	at := strings.LastIndex(raw, "@")
	if at < 0 {
		return raw
	}

	credentials := raw[:at]

	colon := strings.Index(credentials, ":")
	if colon < 0 {
		return raw
	}

	return fmt.Sprintf("%s:%s%s", credentials[:colon], redacted, raw[at:])
}
