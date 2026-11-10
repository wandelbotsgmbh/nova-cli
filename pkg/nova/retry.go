package nova

import (
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/wandelbotsgmbh/nova-cli/pkg/httpx"
)

// createResends is how often a create request is sent again after a dropped connection.
const createResends = 5

// resendDelay is the wait before resend number attempt (replaced in tests).
var resendDelay = httpx.RetryDelay

// createWithRetry sends a create request (POST, which the HTTP transport doesn't
// resend after a drop: it isn't idempotent). The instance ingress reloads while an app
// is deployed and cuts the request off (GOAWAY, EOF), although the instance goes on
// with it. So after a drop the request is sent again: a 409 Conflict then means an
// earlier attempt was accepted, and update (the idempotent PUT with the same
// configuration and completion_timeout) waits for the rollout instead.
func createWithRetry(what string, send func() (*http.Response, []byte, error), update func() error, failure string) error {
	resp, body, err := send()
	resent := false
	for attempt := 0; attempt < createResends && httpx.IsConnectionDrop(err); attempt++ {
		delay := resendDelay(attempt)
		fmt.Fprintf(os.Stderr, "note: the instance closed the connection while creating %s, sending the request again in %s\n", what, delay)
		time.Sleep(delay)
		resp, body, err = send()
		resent = true
	}
	if err != nil {
		return err
	}
	if resent && resp.StatusCode == http.StatusConflict {
		fmt.Fprintf(os.Stderr, "note: %s already exists, an earlier request went through; waiting for the rollout\n", what)
		return update()
	}
	if !StatusSuccessfull(resp.StatusCode) {
		return fmt.Errorf("%s: %s %s", failure, resp.Status, string(body))
	}
	return nil
}
