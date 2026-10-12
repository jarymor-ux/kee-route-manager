package components

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/jarymor-ux/kee-route-manager/internal/config"
)

func TestAWGVersionLookupIgnoresOverflowNumericTags(t *testing.T) {
	client := &http.Client{Transport: fakeRoundTrip(func(_ *http.Request) (*http.Response, error) {
		refs := "[{\"ref\":\"refs/tags/v999999999999999999999.0\"}," +
			"{\"ref\":\"refs/tags/v3.1.20260828\"}]"
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(refs)), Header: http.Header{}}, nil
	})}
	got, err := NewSystemBackend(config.Default(), client).Latest(context.Background(), AmneziaWG)
	if err != nil || got != "3.1.20260828" {
		t.Fatalf("invalid overflow tag overshadowed a valid stable tag: %q / %v", got, err)
	}
}
