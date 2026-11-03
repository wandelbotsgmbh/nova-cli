package simulation

import (
	"fmt"
	"net/url"
	"sort"
)

// SessionAPI is an HTTP API served from inside a running session.
type SessionAPI struct {
	Name    string
	Title   string
	BaseURL string
}

// SpecURL is where the API serves its OpenAPI spec.
func (a SessionAPI) SpecURL() string {
	return a.BaseURL + "/openapi.json"
}

// Kit's default http port; the viewer falls back to it as well.
const defaultAPIPort = 8080

// sessionAPIs are the APIs of a session, the paths the viewer uses.
var sessionAPIs = []SessionAPI{
	{Name: "stream", Title: "Wandelbots Kit Stream API", BaseURL: "/wandelbots-stream/api/v1"},
	{Name: "omniservice", Title: "Wandelbots NOVA - Isaac Sim Extension API", BaseURL: "/omniservice/api/v2"},
}

// SessionAPIs returns the APIs served from inside the session. Like the viewer,
// it takes the first routed host and the route to Kit's http port.
func SessionAPIs(s *SessionResponse) ([]SessionAPI, error) {
	host, routes, err := sessionHost(s)
	if err != nil {
		return nil, err
	}
	port := defaultAPIPort
	for _, r := range routes.Routes {
		if r.DestinationPort == 8011 || r.DestinationPort == 8080 {
			port = int(r.SourcePort)
			break
		}
	}
	apis := make([]SessionAPI, len(sessionAPIs))
	for i, a := range sessionAPIs {
		a.BaseURL = fmt.Sprintf("https://%s:%d%s", host, port, a.BaseURL)
		apis[i] = a
	}
	return apis, nil
}

func sessionHost(s *SessionResponse) (string, RouteInfo, error) {
	hosts := make([]string, 0, len(s.Routes))
	for h := range s.Routes {
		hosts = append(hosts, h)
	}
	if len(hosts) == 0 {
		return "", RouteInfo{}, fmt.Errorf("session %s has no routes yet (status %s, condition %s)", s.Id, s.Status, s.Condition)
	}
	sort.Strings(hosts)
	return hosts[0], s.Routes[hosts[0]], nil
}

// StreamURL is the viewer page streaming the session.
func (e Endpoints) StreamURL(sessionID string) string {
	return e.Viewer + "/stream?id=" + url.QueryEscape(sessionID)
}
