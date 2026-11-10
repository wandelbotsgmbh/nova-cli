package nova

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/wandelbotsgmbh/nova-cli/pkg/httpx"
)

// resendDelay is the wait before resending a create request (replaced in tests).
var resendDelay = httpx.FirstRetryDelay

// createWithRetry sends a create request (POST, which the HTTP transport doesn't
// resend). If the connection dropped mid-request (the instance ingress reloads while
// cells and apps are deployed), it sends the request once more after a short wait: a
// 409 Conflict then means the first attempt was accepted, and waitReady waits for the
// rollout the first request would have waited for (completion_timeout).
func createWithRetry(what string, send func() (*http.Response, []byte, error), waitReady func() error, failure string) error {
	resp, body, err := send()
	retried := false
	if httpx.IsConnectionDrop(err) {
		fmt.Fprintf(os.Stderr, "note: the instance closed the connection while creating %s, sending the request again\n", what)
		time.Sleep(resendDelay())
		resp, body, err = send()
		retried = true
	}
	if err != nil {
		return err
	}
	if retried && resp.StatusCode == http.StatusConflict {
		fmt.Fprintf(os.Stderr, "note: %s already exists, the first request went through; waiting for the rollout\n", what)
		return waitReady()
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

// cellStatusPoll is the interval of waitForCellRunning (replaced in tests).
var cellStatusPoll = 2 * time.Second

// waitForCellRunning waits until every service of the cell is running and, with app,
// that the app is among them; at most the default completion timeout.
func waitForCellRunning(host, cell, app string) error {
	client, err := NewV2ClientWithToken(host)
	if err != nil {
		return err
	}
	timeout := time.Duration(*GetDefaultCompletionTimeoutSeconds()) * time.Second
	deadline := time.Now().Add(timeout)
	last := "no status yet"
	for {
		resp, err := client.GetCellStatusWithResponse(context.Background(), cell)
		switch {
		case err != nil:
			last = err.Error()
		case resp.JSON200 == nil:
			last = resp.Status()
		case len(resp.JSON200.ServiceStatus) == 0:
			// a cell that was only just accepted may not list its services yet
			last = "no services listed yet"
		default:
			ready, foundApp := true, app == ""
			for _, s := range resp.JSON200.ServiceStatus {
				if s.Service == app {
					foundApp = true
				}
				if s.Status.Code != "Running" {
					ready = false
					last = fmt.Sprintf("%s is %s", s.Service, s.Status.Code)
				}
			}
			if ready && foundApp {
				return nil
			}
			if !foundApp {
				last = fmt.Sprintf("app %s not listed yet", app)
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("cell %s not ready after %s (%s)", cell, timeout, last)
		}
		time.Sleep(cellStatusPoll)
	}
}
