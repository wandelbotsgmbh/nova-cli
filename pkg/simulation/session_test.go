package simulation

import (
	"encoding/json"
	"testing"
)

// routes of a dev session as the broker returned them
const sessionJSON = `{"id":"66416162-239c-4aa0-b212-02b1323102cf","status":"active","condition":"ready","ready":true,
"routes":{"vtbwlp.kitstreaming.omni.dev.wandelbots.io":{"routes":[
{"description":"media","protocol":"UDP","sourcePort":47998,"destinationPort":31883},
{"description":"signaling","protocol":"TCP","sourcePort":49100,"destinationPort":30940},
{"description":"health","protocol":"TCP","sourcePort":8080,"destinationPort":30967}]}}}`

func TestSessionAPIs(t *testing.T) {
	tests := []struct {
		name string
		edit func(*SessionResponse)
		want string
	}{
		{"default kit port", func(*SessionResponse) {}, "https://vtbwlp.kitstreaming.omni.dev.wandelbots.io:8080/omniservice/api/v2"},
		{"routed kit port", func(s *SessionResponse) {
			r := s.Routes["vtbwlp.kitstreaming.omni.dev.wandelbots.io"]
			r.Routes = append(r.Routes, StreamRoute{Description: "api", SourcePort: 31000, DestinationPort: 8011})
			s.Routes["vtbwlp.kitstreaming.omni.dev.wandelbots.io"] = r
		}, "https://vtbwlp.kitstreaming.omni.dev.wandelbots.io:31000/omniservice/api/v2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var s SessionResponse
			if err := json.Unmarshal([]byte(sessionJSON), &s); err != nil {
				t.Fatal(err)
			}
			tt.edit(&s)
			apis, err := SessionAPIs(&s)
			if err != nil {
				t.Fatal(err)
			}
			if got := apis[1].BaseURL; got != tt.want {
				t.Errorf("omniservice base url = %q, want %q", got, tt.want)
			}
			if got, want := apis[0].SpecURL(), "https://vtbwlp.kitstreaming.omni.dev.wandelbots.io"; got[:len(want)] != want {
				t.Errorf("stream spec url = %q", got)
			}
		})
	}
}

func TestSessionAPIsWithoutRoutes(t *testing.T) {
	if _, err := SessionAPIs(&SessionResponse{Status: "pending"}); err == nil {
		t.Error("want an error for a session without routes")
	}
}
