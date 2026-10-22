package themebundle

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testArchive(t *testing.T, extra *tar.Header) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "theme.tar.gz")
	file, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(file)
	tr := tar.NewWriter(gz)
	manifest := Manifest{Format: 1, Dependencies: "bundled-portable-v1", Version: "theme-v1.0.0", SourceCommit: strings.Repeat("a", 40), OS: "linux", Arch: "amd64", NodeMajor: 24, PNPM: PNPM, AdminAPI: 1, Settings: 1}
	data, _ := json.Marshal(manifest)
	entries := map[string][]byte{"manifest.json": data}
	for _, name := range []string{"package.json", "pnpm-lock.yaml", "scripts/aigoni-build.mjs", "aigoni-defaults.json", "aigoni-settings-schema.json", "node_modules/astro/package.json"} {
		entries["source/"+name] = []byte("{}")
	}
	for name, body := range entries {
		if err = tr.WriteHeader(&tar.Header{Name: name, Mode: 0644, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err = tr.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if extra != nil {
		if err = tr.WriteHeader(extra); err != nil {
			t.Fatal(err)
		}
	}
	if err = tr.Close(); err != nil {
		t.Fatal(err)
	}
	if err = gz.Close(); err != nil {
		t.Fatal(err)
	}
	file.Close()
	return out
}
func TestExtractBoundaries(t *testing.T) {
	ctx := context.Background()
	m, _, err := Extract(ctx, testArchive(t, nil), filepath.Join(t.TempDir(), "valid"), "theme-v1.0.0")
	if err != nil || m.NodeMajor != 24 {
		t.Fatal(m, err)
	}
	for _, h := range []tar.Header{{Name: "../outside", Typeflag: tar.TypeReg}, {Name: "source/node_modules/escape", Typeflag: tar.TypeSymlink, Linkname: "../../../outside"}, {Name: "source/hard", Typeflag: tar.TypeLink, Linkname: "manifest.json"}, {Name: "source/device", Typeflag: tar.TypeChar}, {Name: "source/scripts/code", Typeflag: tar.TypeSymlink, Linkname: "../package.json"}, {Name: "source/package.json", Typeflag: tar.TypeReg}} {
		if _, _, err = Extract(ctx, testArchive(t, &h), filepath.Join(t.TempDir(), "bad"), "theme-v1.0.0"); err == nil {
			t.Errorf("accepted unsafe entry %+v", h)
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err = Extract(cancelled, testArchive(t, nil), filepath.Join(t.TempDir(), "cancelled"), "theme-v1.0.0"); err == nil {
		t.Fatal("cancel ignored")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestDigestOnlyFromFixedRelease(t *testing.T) {
	tag := "theme-v1.0.0"
	digest := strings.Repeat("a", 64)
	name := AssetName(tag)
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.String() != "https://api.github.com/repos/rrkistch/aigoni/releases/tags/"+tag {
			t.Fatal(req.URL)
		}
		body := `{"tag_name":"` + tag + `","assets":[{"name":"` + name + `","size":12,"digest":"sha256:` + digest + `","browser_download_url":"https://github.com/rrkistch/aigoni/releases/download/` + tag + `/` + name + `"}]}`
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	got, err := TrustedDigest(context.Background(), client, tag, 12)
	if err != nil || got != digest {
		t.Fatal(got, err)
	}
	if _, err = TrustedDigest(context.Background(), client, tag, 13); err == nil {
		t.Fatal("size mismatch accepted")
	}
	if _, err = TrustedDigest(context.Background(), client, "https://untrusted.invalid", 12); err == nil {
		t.Fatal("untrusted source accepted")
	}
}
