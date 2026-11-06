package httpx

import (
	"net/http"
	"testing"
)

// All clients share http.DefaultTransport's connection pool (see Transport).
func TestTransportIsShared(t *testing.T) {
	if Transport().(userAgentTransport).next != http.DefaultTransport {
		t.Error("Transport() must use http.DefaultTransport")
	}
	if Client().Transport.(userAgentTransport).next != http.DefaultTransport {
		t.Error("Client() must use http.DefaultTransport")
	}
}
