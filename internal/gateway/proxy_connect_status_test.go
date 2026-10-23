package gateway

import "testing"

func TestProxyConnectStatus(t *testing.T) {
	tests := []struct {
		name string
		err  string
		want int
	}{
		{name: "407 connect auth required", err: "Post \"https://example.test/inference/v1/chat/completions\": proxyconnect tcp: proxy refused CONNECT: 407 Proxy Authentication Required", want: 407},
		{name: "429 connect limit", err: "proxyconnect tcp: proxy refused CONNECT: 429 Not Enough Connections", want: 429},
		{name: "403 connect denied", err: "proxyconnect tcp: response status code 403 Forbidden", want: 403},
		{name: "ordinary transport failure", err: "dial tcp: connection refused", want: 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := proxyConnectStatus(test.err); got != test.want {
				t.Fatalf("proxyConnectStatus() = %d, want %d", got, test.want)
			}
		})
	}
}
