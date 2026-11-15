package library

import (
	"net"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
)

var clientIPHeaders = []string{
	"X-Original-Client-Ip",
	"X-Client-Ip",
	"Cf-Connecting-Ip",
	"True-Client-Ip",
}

func parsePublicIP(v string) string {

	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "[")
	v = strings.TrimSuffix(v, "]")

	ip := net.ParseIP(v)
	if ip == nil || ip.IsPrivate() || ip.IsLoopback() || ip.IsUnspecified() {
		return ""
	}

	return ip.String()
}

func ExtractIPFromRealIPHeader(options ...echo.TrustOption) echo.IPExtractor {

	return func(req *http.Request) string {

		for _, h := range clientIPHeaders {
			if ip := parsePublicIP(req.Header.Get(h)); ip != "" {
				return ip
			}
		}

		if xff := req.Header.Get("X-Forwarded-For"); xff != "" {
			for _, part := range strings.Split(xff, ",") {
				if ip := parsePublicIP(part); ip != "" {
					return ip
				}
			}
		}

		if host, _, err := net.SplitHostPort(req.RemoteAddr); err == nil {
			return host
		}

		return req.RemoteAddr
	}
}
