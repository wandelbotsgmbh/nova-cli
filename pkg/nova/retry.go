package nova

import (
	"fmt"
	"net/http"
	"os"

	"github.com/wandelbotsgmbh/nova-cli/pkg/httpx"
)

// createWithRetry sends a create request (POST, which the HTTP transport doesn't
// resend). If the connection dropped mid-request (the instance ingress reloads while
// cells and apps are deployed), it sends the request once more: a 409 Conflict then
// means the first attempt was accepted and the instance is applying it.
func createWithRetry(what string, send func() (*http.Response, []byte, error), failure string) error {
	resp, body, err := send()
	retried := false
	if httpx.IsConnectionDrop(err) {
		fmt.Fprintf(os.Stderr, "note: the instance closed the connection while creating %s, sending the request again\n", what)
		resp, body, err = send()
		retried = true
	}
	if err != nil {
		return err
	}
	if retried && resp.StatusCode == http.StatusConflict {
		fmt.Fprintf(os.Stderr, "note: %s already exists, the first request went through; the instance may still be rolling it out\n", what)
		return nil
	}
	if !StatusSuccessfull(resp.StatusCode) {
		return fmt.Errorf("%s: %s %s", failure, resp.Status, string(body))
	}
	return nil
}

// deletedByEarlierAttempt reports whether resp is a 404 to a DELETE that the transport
// resent after a dropped connection, i.e. the first attempt already deleted it.
func deletedByEarlierAttempt(resp *http.Response) bool {
	return resp != nil && resp.StatusCode == http.StatusNotFound && httpx.Retried(resp)
}
