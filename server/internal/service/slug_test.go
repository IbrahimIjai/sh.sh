package service

import (
	"bytes"
	"testing"
)

func TestRandomSlugSkipsBiasedBytes(t *testing.T) {
	slug, err := randomSlugFrom(bytes.NewReader([]byte{248, 255, 0, 61, 62, 247}), 4)
	if err != nil {
		t.Fatal(err)
	}
	if slug != "a9a9" {
		t.Errorf("slug = %q, want a9a9 (bytes 248 and 255 skipped)", slug)
	}
	if _, err := randomSlugFrom(bytes.NewReader([]byte{0, 255}), 4); err == nil {
		t.Error("want an error when the source runs out")
	}
}
