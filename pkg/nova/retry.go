package nova

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	apiv2 "github.com/wandelbotsgmbh/nova-api-client-go/v25/pkg/nova/v2"
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

// stablePolls is how many polls in a row the cell must look ready with the same
// services: the status lists services gradually while a cell rolls out (measured:
// none, then one, then all six within ~2 s), so one "all listed are running" can be a
// partial list.
const stablePolls = 3

// waitForCellRunning waits for the rollout a create with completion_timeout would have
// waited for: every controller and app of the cell's configuration (and app, if given)
// is listed, every listed service is running, and the list stayed the same for
// stablePolls polls. At most the default completion timeout.
func waitForCellRunning(host, cell, app string) error {
	client, err := NewV2ClientWithToken(host)
	if err != nil {
		return err
	}
	expected := map[string]bool{}
	if app != "" {
		expected[app] = true
	}
	if resp, err := client.GetCellWithResponse(context.Background(), cell); err == nil && resp.JSON200 != nil {
		if resp.JSON200.Controllers != nil {
			for _, c := range *resp.JSON200.Controllers {
				expected[c.Name] = true
			}
		}
		if resp.JSON200.Apps != nil {
			for _, a := range *resp.JSON200.Apps {
				expected[a.Name] = true
			}
		}
	}

	timeout := time.Duration(*GetDefaultCompletionTimeoutSeconds()) * time.Second
	deadline := time.Now().Add(timeout)
	last, previous, stable := "no status yet", "", 0
	for {
		ready := false
		resp, err := client.GetCellStatusWithResponse(context.Background(), cell)
		switch {
		case err != nil:
			last = err.Error()
		case resp.JSON200 == nil:
			last = resp.Status()
		case len(resp.JSON200.ServiceStatus) == 0:
			last = "no services listed yet"
		default:
			ready, last = cellReady(resp.JSON200.ServiceStatus, expected)
			if ready {
				set := serviceSet(resp.JSON200.ServiceStatus)
				if set != previous {
					previous, stable = set, 0
				}
				stable++
				if stable >= stablePolls {
					return nil
				}
			}
		}
		if !ready {
			previous, stable = "", 0
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("cell %s not ready after %s (%s)", cell, timeout, last)
		}
		time.Sleep(cellStatusPoll)
	}
}

// cellReady reports whether every expected service is listed and every listed one is
// running; otherwise it says what is missing.
func cellReady(services []apiv2.ServiceStatus, expected map[string]bool) (bool, string) {
	listed := map[string]bool{}
	for _, s := range services {
		listed[s.Service] = true
		if s.Status.Code != "Running" {
			return false, fmt.Sprintf("%s is %s", s.Service, s.Status.Code)
		}
	}
	missing := []string{}
	for name := range expected {
		if !listed[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return false, "not listed yet: " + strings.Join(missing, ", ")
	}
	return true, "all services running"
}

func serviceSet(services []apiv2.ServiceStatus) string {
	names := make([]string, 0, len(services))
	for _, s := range services {
		names = append(names, s.Service)
	}
	sort.Strings(names)
	return strings.Join(names, ",")
}
