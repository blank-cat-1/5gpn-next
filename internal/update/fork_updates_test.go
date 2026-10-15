package update

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type releaseTransport func(*http.Request) (*http.Response, error)

func (f releaseTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestForkChecksUseOnlyOwnRepository(t *testing.T) {
	if repo != "blank-cat-1/5gpn-next" {
		t.Fatalf("release source must be the fork, got %q", repo)
	}
	for _, tc := range []struct {
		current string
		want    bool
	}{
		{"v0.13.27-custom.20261016.2", true},
		{"v0.13.27-custom.20261016.10", false},
	} {
		m := New(tc.current)
		m.client = &http.Client{Transport: releaseTransport(func(r *http.Request) (*http.Response, error) {
			if r.Method != http.MethodGet || r.URL.Scheme != "https" || r.URL.Host != "api.github.com" || r.URL.Path != "/repos/blank-cat-1/5gpn-next/releases/latest" {
				t.Fatalf("unexpected update endpoint: %s %s", r.Method, r.URL)
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"tag_name":"v0.13.27-custom.20261016.10","name":"Fixture release"}`))}, nil
		})}
		has, rel, err := m.HasUpdate(context.Background())
		if err != nil || has != tc.want || rel == nil || rel.Tag != "v0.13.27-custom.20261016.10" {
			t.Fatalf("current=%s has=%v rel=%+v err=%v", tc.current, has, rel, err)
		}
		if cached, _, ok := m.Cached(); !ok || cached.Tag != rel.Tag {
			t.Fatal("successful fork check was not available to the Bot menu")
		}
	}
}

func TestCustomReleaseOrdering(t *testing.T) {
	for _, tc := range []struct {
		candidate, current string
		want               bool
	}{
		{"v0.13.27-custom.20261016.2", "v0.13.27-custom.20261016.1", true},
		{"v0.13.27-custom.20261016.10", "v0.13.27-custom.20261016.2", true},
		{"v0.13.27-custom.20261016.2", "v0.13.27-custom.20261016.10", false},
		{"v0.13.27-custom.20261017.1", "v0.13.27-custom.20261016.99", true},
		{"v0.13.27-custom.20261015.2", "v0.13.27-custom.20261016.1", false},
		{"v0.13.27-custom.20261016.2", "v0.13.27-custom.20261016.2", false},
		{"v0.13.27-custom.20261016.2+build2", "v0.13.27-custom.20261016.2+build1", false},
		{"v0.13.28-custom.20261016.1", "v0.13.27-custom.20261016.2", true},
		{"v0.13.28", "v0.13.28-rc.1", true},
		{"v0.13.28-rc.10", "v0.13.28-rc.2", true},
		{"v0.13.28-rc.1", "v0.13.28", false},
		{"v0.13.27-custom.20261016.2", "dev", true},
	} {
		if got := Newer(tc.candidate, tc.current); got != tc.want {
			t.Errorf("Newer(%q, %q)=%v want %v", tc.candidate, tc.current, got, tc.want)
		}
	}
}

func TestApplyRejectsSameOrOlderCustomReleaseBeforeDownloads(t *testing.T) {
	m := New("v0.13.27-custom.20261016.10")
	m.client = &http.Client{Transport: forbiddenDownloadTransport{t}}
	for _, tag := range []string{"v0.13.27-custom.20261016.10", "v0.13.27-custom.20261016.9", "v0.13.27-custom.20261015.2"} {
		if _, err := m.Apply(context.Background(), tag); err == nil {
			t.Fatalf("accepted same or older custom release %q", tag)
		}
	}
}
