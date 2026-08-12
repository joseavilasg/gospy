package rules

import (
	"io"
	"net/http"
	"strings"
)

// BuildMockResponse returns the response described by a rule's mock_response,
// or a plain 200 empty response when the mock is nil. The response is
// framing-neutral: consumers decide how the body is written on the wire
// (goproxy chunk-encodes OnRequest responses; the replay server frames from
// the ContentLength field).
func BuildMockResponse(req *http.Request, mock *MockResponse) *http.Response {
	status := 200
	headers := http.Header{}
	body := ""

	if mock != nil {
		status = mock.Status
		if status == 0 {
			status = 200
		}
		for k, vals := range mock.Headers {
			for _, v := range vals {
				headers.Set(k, v)
			}
		}
		body = mock.Body
	}

	return &http.Response{
		StatusCode: status,
		Header:     headers,
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
}

// BuildDropResponse returns the silent-drop response (504 Gateway Timeout with
// an empty body) used by the drop action. Framing-neutral, like
// BuildMockResponse.
func BuildDropResponse(req *http.Request) *http.Response {
	return &http.Response{
		StatusCode: 504,
		Status:     "504 Gateway Timeout",
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("")),
		Request:    req,
	}
}
