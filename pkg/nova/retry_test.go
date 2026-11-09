package nova

import (
	"errors"
	"net/http"
	"testing"
)

func TestCreateWithRetry(t *testing.T) {
	drop := errors.New(`Post "https://x/api/v2/cells": http2: server sent GOAWAY and closed the connection; LastStreamID=5`)
	answer := func(status int) (*http.Response, []byte, error) {
		return &http.Response{StatusCode: status, Status: http.StatusText(status)}, []byte("body"), nil
	}
	cases := []struct {
		name    string
		answers []func() (*http.Response, []byte, error)
		wantErr bool
		calls   int
	}{
		{"ok", []func() (*http.Response, []byte, error){func() (*http.Response, []byte, error) { return answer(201) }}, false, 1},
		{"conflict without drop fails", []func() (*http.Response, []byte, error){func() (*http.Response, []byte, error) { return answer(409) }}, true, 1},
		{"drop then conflict means created", []func() (*http.Response, []byte, error){
			func() (*http.Response, []byte, error) { return nil, nil, drop },
			func() (*http.Response, []byte, error) { return answer(409) }}, false, 2},
		{"drop then created", []func() (*http.Response, []byte, error){
			func() (*http.Response, []byte, error) { return nil, nil, drop },
			func() (*http.Response, []byte, error) { return answer(201) }}, false, 2},
		{"drop twice fails", []func() (*http.Response, []byte, error){
			func() (*http.Response, []byte, error) { return nil, nil, drop },
			func() (*http.Response, []byte, error) { return nil, nil, drop }}, true, 2},
		{"other error not retried", []func() (*http.Response, []byte, error){
			func() (*http.Response, []byte, error) { return nil, nil, errors.New("no such host") }}, true, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			calls := 0
			err := createWithRetry("cell x", func() (*http.Response, []byte, error) {
				calls++
				return c.answers[calls-1]()
			}, "failed to create cell")
			if (err != nil) != c.wantErr || calls != c.calls {
				t.Errorf("err=%v calls=%d", err, calls)
			}
		})
	}
}

func TestDeletedByEarlierAttempt(t *testing.T) {
	retried := &http.Response{StatusCode: 404, Header: http.Header{"X-Nova-Cli-Retried": {"1"}}}
	first := &http.Response{StatusCode: 404, Header: http.Header{}}
	if !deletedByEarlierAttempt(retried) || deletedByEarlierAttempt(first) {
		t.Error("only a 404 to a resent DELETE counts as deleted")
	}
}
