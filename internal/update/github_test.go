package update

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestStableVersions(t *testing.T) {
	for _, entry := range []struct {
		next, installed string
		want            bool
	}{
		{"0.0.2", "0.0.1", true},
		{"0.0.10", "0.0.9", true},
		{"1.0.0", "0.12.99", true},
		{"0.0.1", "0.0.1", false},
		{"0.0.9", "0.0.10", false},
		{"0.0.2-beta.1", "0.0.1", false},
		{"0.0.02", "0.0.1", false},
	} {
		if got := newer(entry.next, entry.installed); got != entry.want {
			t.Errorf("newer(%q, %q) = %v, want %v", entry.next, entry.installed, got, entry.want)
		}
	}
}

func TestCheckAndVerifyOfflineInstaller(t *testing.T) {
	const repository = "oddmeta/shengji"
	const stable = "v0.0.2"
	const asset = "Shengji-amd64-offline-installer.exe"
	const installerBytes = "installer fixture"
	const installerURL = "https://github.com/" + repository + "/releases/download/" + stable + "/" + asset
	const releaseURL = "https://api.github.com/repos/" + repository + "/releases/latest"
	digest := sha256.Sum256([]byte(installerBytes))
	for _, entry := range []struct {
		name, url, body string
		badMetadata     bool
		badDownload     bool
	}{
		{"valid", installerURL, installerBytes, false, false},
		{"wrong URL", "https://example.test/installer.exe", installerBytes, true, false},
		{"wrong digest", installerURL, "Installer fixture", false, true},
		{"too long", installerURL, installerBytes + "!", false, true},
	} {
		t.Run(entry.name, func(t *testing.T) {
			httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				var body string
				switch r.URL.String() {
				case releaseURL:
					body = fmt.Sprintf(`{"tag_name":%q,"assets":[{"name":"Shengji-amd64-installer.exe"},{"name":%q,"browser_download_url":%q,"digest":"sha256:%x","size":%d}]}`,
						stable, asset, entry.url, digest, len(installerBytes))
				case installerURL:
					body = entry.body
				default:
					return nil, fmt.Errorf("unexpected request %s", r.URL)
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), ContentLength: -1}, nil
			})}
			installer, err := check(context.Background(), httpClient, repository, "0.0.1", "amd64", true)
			if entry.badMetadata {
				if err == nil || installer != nil {
					t.Fatalf("accepted invalid release: %+v, %v", installer, err)
				}
				return
			}
			if err != nil || installer == nil || installer.Version != "0.0.2" || installer.name != asset {
				t.Fatalf("unexpected release: %+v, %v", installer, err)
			}
			path, err := installer.download(context.Background(), httpClient)
			if path != "" {
				t.Cleanup(func() { Remove(path) })
			}
			if entry.badDownload {
				if err == nil || path != "" {
					t.Fatalf("accepted invalid installer: %q, %v", path, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != installerBytes {
				t.Fatalf("unexpected staged installer: %q, %v", data, err)
			}
		})
	}
}
