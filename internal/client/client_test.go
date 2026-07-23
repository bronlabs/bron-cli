package client

import (
	"errors"
	"testing"

	sdkhttp "github.com/bronlabs/bron-sdk-go/sdk/http"

	"github.com/bronlabs/bron-api-toolkit/mcptools"
)

func TestWrapAPIErrorMapsSDKError(t *testing.T) {
	src := &sdkhttp.APIError{Status: 409, Code: "conflict", Message: "external id taken", RequestID: "req-9"}
	var out *mcptools.APIError
	if !errors.As(WrapAPIError(src), &out) {
		t.Fatal("WrapAPIError did not yield *mcptools.APIError")
	}
	if out.Status != 409 || out.Code != "conflict" || out.Message != "external id taken" || out.RequestID != "req-9" {
		t.Fatalf("fields not preserved: %+v", out)
	}
}

func TestWrapAPIErrorPassesThroughNonAPIError(t *testing.T) {
	base := errors.New("dial tcp: timeout")
	if got := WrapAPIError(base); got != base {
		t.Fatalf("non-APIError should pass through unchanged, got %v", got)
	}
}
