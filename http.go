package retry

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/ioutil"
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
// • The 501 "Not Implemented" status code is treated like as permanent
// failure.
//
// HTTP 4xx errors are generally not retried (and therefore
// don't result in an error being returned), with two exceptions:
//
// • The 423 "Locked" status code is treated like a temporary issue.
//
// • When the response has a 4xx status code and the "Retry-After" header the
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
	for _, opt := range opts {
		t.opts = append(t.opts, opt)
	}

	return t
}

func checkResponse(res *http.Response, err error) error {
	if err != nil {
		if _, ok := err.(Error); ok {
			return err
		}
		return Abort(err)
	}

	// special cases
	if res.StatusCode == http.StatusNotImplemented {
		// permanent condition, don't retry
		return nil
	} else if res.StatusCode == http.StatusLocked {
		// temporary condition, retry
		return errors.New(res.Status)
	} else if res.StatusCode >= 500 && res.StatusCode < 600 {
		// temporary condition, retry
		return errors.New(res.Status)
	} else if res.StatusCode >= 400 && res.StatusCode < 500 {
		if _, ok := res.Header["Retry-After"]; ok {
			// temporary condition, retry
			return errors.New(res.Status)
		}
		// else: permanent condition, don't retry
		return nil
	}

	return nil
}

// RoundTrip implements a retrying "net/http".RoundTripper.
func (t Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	var body io.ReadSeeker
	if req.Body != nil {
		defer req.Body.Close()
		if rs, ok := req.Body.(io.ReadSeeker); ok {
			body = rs
		} else {
			data, err := ioutil.ReadAll(req.Body)
			if err != nil {
				return nil, err
			}
			body = bytes.NewReader(data)
		}
	}

	opts := t.opts
	if opts == nil {
		opts = []Option{}
	}

	var ret *http.Response
	err := Do(req.Context(), func(ctx context.Context) error {
		rt := t.RoundTripper
		if rt == nil {
			rt = http.DefaultTransport
		}

		if body != nil {
			body.Seek(0, io.SeekStart)
			req.Body = ioutil.NopCloser(body)
		}

		if a := Attempt(ctx); a > 0 {
			req.Header.Set("Retry-Attempt", strconv.Itoa(a))
		}

		res, err := rt.RoundTrip(req.WithContext(ctx))
		if err := checkResponse(res, err); err != nil {
			return err
		}

		ret = res
		return nil
	}, opts...)
	if err != nil {
		return nil, err
	}

	return ret, nil
}
