package billers

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"testing"
)

// A Fetcher that answers from a script and remembers what it was asked.
//
// Steps are consumed in order, and a call beyond the script fails the test,
// so a module that makes an extra request is caught rather than answered.
type scriptedFetch struct {
	t     *testing.T
	steps []scriptedStep
	calls []scriptedCall
}

type scriptedStep struct {
	status int
	body   string
	// contentType is "application/json" unless a step says otherwise.
	contentType string
	// err stands in for a call that never reached the provider.
	err error
}

type scriptedCall struct {
	Method  string
	URL     string
	Headers http.Header
	Body    string
}

func scripted(t *testing.T, steps ...scriptedStep) *scriptedFetch {
	return &scriptedFetch{t: t, steps: steps}
}

func jsonStep(status int, body string) scriptedStep {
	return scriptedStep{status: status, body: body, contentType: "application/json"}
}

func pdfStep() scriptedStep {
	return scriptedStep{status: 200, body: "%PDF-1.4 invented statement", contentType: "application/pdf"}
}

func (f *scriptedFetch) Do(req *http.Request) (*http.Response, error) {
	call := scriptedCall{Method: req.Method, URL: req.URL.String(), Headers: req.Header.Clone()}
	if req.Body != nil {
		raw, _ := io.ReadAll(req.Body)
		call.Body = string(raw)
	}
	f.calls = append(f.calls, call)
	if len(f.steps) == 0 {
		f.t.Fatalf("unexpected call to %s %s", req.Method, req.URL)
	}
	step := f.steps[0]
	f.steps = f.steps[1:]
	if step.err != nil {
		return nil, step.err
	}
	contentType := step.contentType
	if contentType == "" {
		contentType = "application/json"
	}
	return &http.Response{
		StatusCode: step.status,
		Status:     fmt.Sprintf("%d", step.status),
		Header:     http.Header{"Content-Type": []string{contentType}},
		Body:       io.NopCloser(bytes.NewReader([]byte(step.body))),
		Request:    req,
	}, nil
}

// last is the call just made, for the assertions that are about the request.
func (f *scriptedFetch) last() scriptedCall { return f.calls[len(f.calls)-1] }
