package nucleus

import (
	"context"
	"testing"
)

func TestDialMissingHost(t *testing.T) {
	ctx := context.Background()
	_, err := Dial(ctx, ClientConfig{APIToken: "tok"})
	if err == nil {
		t.Fatal("expected error for missing host")
	}
}

func TestDialMissingToken(t *testing.T) {
	ctx := context.Background()
	_, err := Dial(ctx, ClientConfig{Host: "localhost"})
	if err == nil {
		t.Fatal("expected error for missing token")
	}
}

func TestEntryHelpers(t *testing.T) {
	e := Entry{
		PathType:          "folder",
		CreatedTimestamp:  1700000000,
		ModifiedTimestamp: 1700001000,
	}
	if !e.IsDir() {
		t.Error("folder should be dir")
	}
	if e.CreatedTime().Unix() != 1700000000 {
		t.Error("wrong created time")
	}
	if e.ModifiedTime().Unix() != 1700001000 {
		t.Error("wrong modified time")
	}

	e2 := Entry{PathType: "file"}
	if e2.IsDir() {
		t.Error("file should not be dir")
	}

	e3 := Entry{PathType: "mount"}
	if !e3.IsDir() {
		t.Error("mount should be dir")
	}
}
