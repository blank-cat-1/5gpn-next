package update

import (
	"context"
	"net/http"
	"testing"
)

type forbiddenDownloadTransport struct{ t *testing.T }

func (f forbiddenDownloadTransport) RoundTrip(*http.Request) (*http.Response, error) {
	f.t.Fatal("manual build attempted a network download")
	return nil, nil
}

func TestManualBuildRejectsRemoteUpdateBeforeNetworkOrFilesystemWork(t *testing.T) {
	previous := repo
	repo = ""
	defer func() { repo = previous }()
	m := New("v0.13.27-custom.20261015.1")
	m.client = &http.Client{Transport: forbiddenDownloadTransport{t}}
	if _, err := m.Latest(context.Background()); err == nil {
		t.Fatal("manual build accepted latest check")
	}
	if _, err := m.Apply(context.Background(), "v99.0.0"); err == nil {
		t.Fatal("manual build accepted upstream installation")
	}
}
