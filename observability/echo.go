// Package observability wires an Echo service into the ChopWin Grafana stack
// (Prometheus metrics + Loki logs) with a single call.
//
// It installs, in one place so it can't drift across services:
//   - structured JSON logging to stdout (Alloy ships stdout → Loki);
//   - a per-request access log as JSON (method/status/latency/uri/...), on a
//     dedicated logger so high-volume access logs go only to stdout, not to any
//     tracing hook the service may have added to the global logrus;
//   - a Prometheus /metrics endpoint (subsystem "echo", the fleet-wide convention
//     so every service emits the same echo_requests_total / _duration_seconds);
//   - gzip that SKIPS /metrics — the Prometheus client already gzips its output,
//     and a second pass double-compresses it, making Prometheus fail the scrape
//     with `expected a valid start token, got "\x1f"`.
//
// Distributed tracing is intentionally NOT touched here — the caller keeps its own
// tracer/exporter setup.
package observability

import (
	"os"
	"time"

	"github.com/labstack/echo-contrib/echoprometheus"
	"github.com/labstack/echo/v4"
	echomw "github.com/labstack/echo/v4/middleware"
	"github.com/sirupsen/logrus"
)

// Options configures Setup. The zero value is valid and gives sensible defaults.
type Options struct {
	// MetricsSubsystem is the Prometheus metric name subsystem. Keep it identical
	// across all services so metric names are uniform fleet-wide. Default "echo".
	MetricsSubsystem string
	// MetricsPath is where metrics are exposed and what gzip / the access log skip.
	// Default "/metrics".
	MetricsPath string
	// DisableGzip skips installing the gzip middleware. Set this only if the service
	// installs its own gzip — in which case that gzip MUST skip MetricsPath itself,
	// or Prometheus scrapes will break (double-gzip).
	DisableGzip bool
}

func (o *Options) applyDefaults() {
	if o.MetricsSubsystem == "" {
		o.MetricsSubsystem = "echo"
	}
	if o.MetricsPath == "" {
		o.MetricsPath = "/metrics"
	}
}

// jsonFormatter is the shared logrus JSON layout: `timestamp`/`level`/`message`
// keys so Loki/Grafana parse them consistently.
func jsonFormatter() *logrus.JSONFormatter {
	return &logrus.JSONFormatter{
		TimestampFormat: time.RFC3339Nano,
		FieldMap: logrus.FieldMap{
			logrus.FieldKeyTime:  "timestamp",
			logrus.FieldKeyLevel: "level",
			logrus.FieldKeyMsg:   "message",
		},
	}
}

// Setup installs JSON logging, the access log, the Prometheus /metrics endpoint,
// and (unless disabled) gzip that skips /metrics.
//
// Call it AFTER the service's RequestID middleware so the access log can capture
// the request id, and remove the service's own gzip / access-logger / metrics
// wiring. Tracing hooks the service adds to the global logrus keep working — this
// only sets the global formatter and output.
func Setup(e *echo.Echo, opts Options) {
	opts.applyDefaults()
	skipMetrics := func(c echo.Context) bool { return c.Path() == opts.MetricsPath }

	// Global logrus → JSON on stdout.
	logrus.SetFormatter(jsonFormatter())
	logrus.SetOutput(os.Stdout)

	// Gzip that skips the metrics path.
	if !opts.DisableGzip {
		e.Use(echomw.GzipWithConfig(echomw.GzipConfig{Skipper: skipMetrics}))
	}

	// Per-request access log as JSON via a dedicated logger (no tracing hook).
	access := logrus.New()
	access.SetFormatter(jsonFormatter())
	access.SetOutput(os.Stdout)
	e.Use(echomw.RequestLoggerWithConfig(echomw.RequestLoggerConfig{
		Skipper:      skipMetrics,
		LogMethod:    true,
		LogURI:       true,
		LogStatus:    true,
		LogLatency:   true,
		LogRemoteIP:  true,
		LogUserAgent: true,
		LogRequestID: true,
		LogError:     true,
		HandleError:  true,
		LogValuesFunc: func(c echo.Context, v echomw.RequestLoggerValues) error {
			entry := access.WithFields(logrus.Fields{
				"type":       "access",
				"method":     v.Method,
				"uri":        v.URI,
				"status":     v.Status,
				"latency_ms": float64(v.Latency.Nanoseconds()) / 1e6,
				"remote_ip":  v.RemoteIP,
				"user_agent": v.UserAgent,
				"request_id": v.RequestID,
			})
			switch {
			case v.Error != nil:
				entry.WithField("error", v.Error.Error()).Error("request")
			case v.Status >= 500:
				entry.Error("request")
			case v.Status >= 400:
				entry.Warn("request")
			default:
				entry.Info("request")
			}
			return nil
		},
	}))

	// Prometheus metrics middleware + endpoint.
	e.Use(echoprometheus.NewMiddleware(opts.MetricsSubsystem))
	e.GET(opts.MetricsPath, echoprometheus.NewHandler())
}
