// Package nucleus provides a pure-Go client for NVIDIA Omniverse Nucleus servers.
//
// It implements the two wire protocols Nucleus uses (SOWS for auth/discovery,
// connlib/JSON-over-WebSocket for the content API) so it works on any platform
// including macOS — no C++ library required.
//
// # Quick Start
//
//	client, err := nucleus.Dial(ctx, nucleus.ClientConfig{
//	    Host:     "nucleus.example.com",
//	    APIToken: os.Getenv("NUCLEUS_TOKEN"),
//	    TLS:      true,
//	})
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer client.Close()
//
//	entries, err := client.List(ctx, "/Projects/")
package nucleus
