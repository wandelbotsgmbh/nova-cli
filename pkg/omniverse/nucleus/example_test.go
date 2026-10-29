package nucleus

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"testing"
	"time"
)

// loadEnv reads KEY="value" pairs from a .env file into the environment.
func loadEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		val = strings.Trim(val, `"`)
		os.Setenv(key, val)
	}
}

func TestExampleDial(t *testing.T) {
	t.Skip("requires a real Nucleus server — run manually")

	loadEnv(".env")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := Dial(ctx, ClientConfig{
		Host:     "nucleus.dev.wandelbots.io",
		APIToken: os.Getenv("NUCLEUS_TOKEN"),
		TLS:      true,
	})
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	fmt.Printf("Connected as %s\n", client.Info().Username)

	// List root directory
	entries, err := client.List(ctx, "/")
	if err != nil {
		log.Fatal(err)
	}
	for _, e := range entries {
		fmt.Printf("  %s (%s)\n", e.Path, e.PathType)
	}

	// Stat a specific path
	entry, err := client.Stat(ctx, "/Projects/")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Projects: type=%s, modified=%s\n", entry.PathType, entry.ModifiedTime())
}
