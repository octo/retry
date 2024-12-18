package retry

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
)

// Transport is a retrying "net/http".RoundTripper. The zero value of Transport
// is a valid "net/http".RoundTripper that is using
// "net/http".DefaultTransport.
//
// Custom options can be set by initializing Transport with NewTransport().
//
// One consequence of using this transport is that HTTP 5xx errors will be
// reported as errors, with one exception:
//
// • The 501 "Not Implemented" status code is treated as a permanent failure.
//
// HTTP 4xx errors are generally not retried (and therefore
// don't result in an error being returned), with two exceptions:
//
// • The 423 "Locked" status code is treated like a temporary issue.
//
// • If the response has a 4xx status code and the "Retry-After" header, the
// request is retried.
//
// Transport needs to be able to read the request body multiple times.
// Depending on the provided Request.Body, this happens in one of two ways:
//
// • If Request.Body implements the io.Seeker interface, Body is rewound by
// calling Seek().
//
// • Otherwise, Request.Body is copied into an internal buffer, which consumes
// additional memory.
//
// When re-sending HTTP requests the transport adds the "Retry-Attempt" HTTP
// header indicating that a request is a retry. The header value is an integer
// counting the retries, i.e. "1" for the first retry (the second attempt
// overall). Note: there is currently no standard or even de-facto standard way
// of indicating retries to an HTTP server. When an appropriate RFC is
// published or an industry standard emerges, this header will be changed
// accordingly.
//
// Use "net/http".Request.WithContext() to pass a context to Do(). By default,
// the request is associated with the background context.
type Transport struct {
	http.RoundTripper

	opts []Option
}

// NewTransport initializes a new Transport with the provided options.
//
// base may be nil in which case it defaults to "net/http".DefaultTransport.
func NewTransport(base http.RoundTripper, opts ...Option) *Transport {
	t := &Transport{
		RoundTripper: base,
	}
	t.opts = append(t.opts, opts...)

	return t
}

func temporaryErrorCode(c int) bool {
	return (c >= 500 && c < 600 && c != http.StatusNotImplemented) ||
		c == http.StatusLocked
}

func permanentErrorCode(c int) bool {
	return (c >= 400 && c < 500 && c != http.StatusLocked) ||
		c == http.StatusNotImplemented
}

// checkResponse checks the HTTP response for retryable errors.
//
// Temporary errors are returned as an error and are therefore retried.
//
// Permanent errors are *not* returned as an error and are therefore *not* retried.
// An argument could be made to return them as a permanent error, too.
// However, this would mean a significant diversion from the standard net/http semantic.
//
// If err is not nil, it is wrapped in permanentError and returned.
func checkResponse(res *http.Response, err error) error {
	if err != nil {
		if _, ok := err.(Error); ok {
			return err
		}
		return Abort(err)
	}

	if temporaryErrorCode(res.StatusCode) {
		return errors.New(res.Status)
	} else if permanentErrorCode(res.StatusCode) {
		if _, ok := res.Header["Retry-After"]; ok {
			// temporary condition, retry
			return errors.New(res.Status)
		}
	}

	return nil
}

// RoundTrip implements a retrying "net/http".RoundTripper.
func (t Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		defer req.Body.Close()
	}

	var (
		body     = seekableBody(req)
		response *http.Response
	)

	err := Do(req.Context(), func(ctx context.Context) error {
		rt := t.RoundTripper
		if rt == nil {
			rt = http.DefaultTransport
		}

		if body != nil {
			if _, err := body.Seek(0, io.SeekStart); err != nil {
				return fmt.Errorf("rewinding request body: %w", err)
			}

			req.Body = io.NopCloser(body)
		}

		if a := Attempt(ctx); a > 0 {
			req.Header.Set("Retry-Attempt", strconv.Itoa(a))
		}

		res, err := rt.RoundTrip(req.WithContext(ctx))
		if err := checkResponse(res, err); err != nil {
			return err
		}

		response = res

		return nil
	}, t.opts...)

	if err != nil {
		return nil, err
	}

	return response, nil
}

func seekableBody(req *http.Request) io.ReadSeeker {
	if req.Body == nil {
		return nil
	}

	if rs, ok := req.Body.(io.ReadSeeker); ok {
		return rs
	}

	// If the body is not a ReadSeeker, read it entirely and create a new ReadSeeker
	data, err := io.ReadAll(req.Body)
	if err != nil {
		return nil
	}

	return bytes.NewReader(data)
}

// BudgetHandler wraps an http.Handler and applies a server-side retry budget.
// When the ratio of retries exceeds BudgetHandler.Ratio while the rate of
// requests is at least BudgetHandler.Rate, i.e. when the retry budget is
// exhausted, then temporary errors are changed to permanent errors.
// A high ratio of retries is an indicator that the cluster as a whole is
// overloaded. Returning permanent errors in an overload situation mitigates
// the risk that retries are keeping the system in overload.
//
// An HTTP request is considered a retry if it has the "Retry-Attempt" HTTP
// header set, as created by Transport. The value of the header is not
// relevant, as long as it is not empty.
//
// Temporary errors are primarily responses with 5xx status codes, but there
// are exceptions. See the documentation of "Transport" type for a detailed
// discussion.
//
// When in an overload situation, BudgetHandler:
//
// • sets the status code to 429 "Too Many Requests" if the status code
// indicates a temporary failure, and
//
// • removes the "Retry-After" header if set.
//
// Note that this is not a rate limiter. BudgetHandler will never decline a
// request itself, it only makes sure that if a request is declined, for
// example with 503 "Service Unavailable", the status code is upgraded to a
// permanent error when the retry budget is exhausted, i.e. when in overload.
type BudgetHandler struct {
	http.Handler

	// Budget is the server side retry budget. While Handler is handling
	// fewer than Budget.Rate requests, responses are never modified. If
	// the ratio of retries to total requests exceeds Budget.Ratio, this is
	// taken as an indicator that the cluster as a whole is overloaded.
	Budget
}

// ServeHTTP proxies the HTTP request to the embedded http.Handler.
func (h *BudgetHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	isRetry := req.Header.Get("Retry-Attempt") != ""

	if h.overload(isRetry) {
		h.Handler.ServeHTTP(&overloadResponseWriter{
			ResponseWriter: w,
		}, req)
	} else {
		h.Handler.ServeHTTP(w, req)
	}
}

type overloadResponseWriter struct {
	http.ResponseWriter
}

func (w *overloadResponseWriter) WriteHeader(statusCode int) {
	w.Header().Del("Retry-After")
	if temporaryErrorCode(statusCode) {
		statusCode = http.StatusTooManyRequests
	}

	w.ResponseWriter.WriteHeader(statusCode)
}
