package library

import (
	"net/http/httptest"
	"testing"
)

func TestExtractIPFromRealIPHeader(t *testing.T) {

	cases := []struct {
		name    string
		headers map[string]string
		remote  string
		want    string
	}{
		{"cf header", map[string]string{"Cf-Connecting-Ip": "41.58.1.2"}, "10.0.0.1:1234", "41.58.1.2"},
		{"header priority", map[string]string{"True-Client-Ip": "8.8.8.8", "X-Original-Client-Ip": "1.1.1.1"}, "10.0.0.1:1", "1.1.1.1"},
		{"private header falls through to xff", map[string]string{"Cf-Connecting-Ip": "10.1.1.1", "X-Forwarded-For": "8.8.4.4"}, "10.0.0.1:1", "8.8.4.4"},
		{"xff leftmost public", map[string]string{"X-Forwarded-For": "192.168.1.5, 102.89.3.4, 8.8.8.8"}, "10.0.0.1:1", "102.89.3.4"},
		{"xff all private falls back to remote", map[string]string{"X-Forwarded-For": "10.0.0.2, 127.0.0.1"}, "172.16.0.9:5555", "172.16.0.9"},
		{"ipv6 bracketed header", map[string]string{"X-Client-Ip": "[2001:4860:4860::8888]"}, "10.0.0.1:1", "2001:4860:4860::8888"},
		{"invalid header", map[string]string{"X-Client-Ip": "not-an-ip"}, "8.8.8.8:80", "8.8.8.8"},
		{"ipv6 remote", nil, "[2001:db8::1]:443", "2001:db8::1"},
		{"remote without port", nil, "8.8.8.8", "8.8.8.8"},
		{"x-real-ip ignored", map[string]string{"X-Real-Ip": "8.8.8.8"}, "10.0.0.1:1", "10.0.0.1"},
	}

	extract := ExtractIPFromRealIPHeader()

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			req.RemoteAddr = c.remote
			for k, v := range c.headers {
				req.Header.Set(k, v)
			}
			if got := extract(req); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}
