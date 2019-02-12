package retry

import (
	"context"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

type testTransport struct {
	status []int
}

func (t *testTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body == nil {
		return nil, errors.New("req.Body == nil")
	}
	defer req.Body.Close()

	if a := Attempt(req.Context()); a != 0 {
		if got, want := req.Header.Get("Retry-Attempt"), strconv.Itoa(a); got != want {
			return nil, fmt.Errorf("req.Header.Get(\"Retry-Attempt\") = %q, want %q", got, want)
		}
	}

	payload, err := ioutil.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	if got, want := string(payload), "request payload"; got != want {
		return nil, fmt.Errorf("request payload: got %q, want %q", got, want)
	}

	if len(t.status) == 0 {
		return nil, errors.New("no more status codes")
	}

	res := &http.Response{}
	res.StatusCode, t.status = t.status[0], t.status[1:]
	return res, nil
}

func TestTransport(t *testing.T) {
	cases := []struct {
		transport  *testTransport
		opts       []Option
		wantStatus int
		wantErr    bool
	}{
		{
			transport:  &testTransport{status: []int{500, 200}},
			wantStatus: 200,
		},
		{
			transport:  &testTransport{status: []int{599, 403}},
			wantStatus: 403,
		},
		{
			transport: &testTransport{status: []int{503, 502, 200}},
			opts:      []Option{Attempts(2)},
			wantErr:   true,
		},
	}

	for _, c := range cases {
		client := &http.Client{
			Transport: NewTransport(c.transport, c.opts...),
		}

		res, err := client.Post("http://example.com/", "text/plain", strings.NewReader("request payload"))
		if err != nil {
			if !c.wantErr {
				t.Errorf("Get() = %v, want success", err)
			}
			continue
		}
		if err == nil && c.wantErr {
			t.Errorf("Get() = %v, want failure", err)
			continue
		}

		if res.StatusCode != c.wantStatus {
			t.Errorf("Get().StatusCode = %d, want %d", res.StatusCode, c.wantStatus)
			continue
		}
	}
}

func ExampleTransport() {
	c := &http.Client{
		Transport: &Transport{},
	}

	// Caveat: there is no specific context associated with this request.
	// The net/http package uses the background context in that case.
	// That means that this request will be retried indefinitely until it succeeds.
	res, err := c.Get("http://example.com/")
	if err != nil {
		log.Fatal(err)
	}
	defer res.Body.Close()

	if res.StatusCode >= 500 && res.StatusCode < 600 {
		panic("this does not happen. HTTP 5xx errors are reported as errors.")
	}

	// use "res"
}

func ExampleTransport_withTimeout() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	c := &http.Client{
		Transport: &Transport{},
	}

	// The context needs to be added to the request via http.Request.WithContext().
	// The net/http package defaults to using the background context, which is never cancelled.
	// That's why NewRequest()/Do() is used here instead of the more
	// convenient Get(), Head() and Post() short-hands.
	req, err := http.NewRequest(http.MethodPost, "https://example.com/",
		strings.NewReader(`{"example":true}`))
	if err != nil {
		log.Fatalf("NewRequest() = %v", err)
	}
	res, err := c.Do(req.WithContext(ctx))
	if err != nil {
		log.Printf("Do() = %v", err)
		return
	}
	defer res.Body.Close()

	// use "res"
}

func ExampleTransport_withOptions() {
	c := &http.Client{
		Transport: NewTransport(http.DefaultTransport, Attempts(3)),
	}

	// Caveat: there is no specific context associated with this request.
	// The net/http package uses the background context, i.e. cancellation does not work.
	res, err := c.Get("http://example.com/")
	if err != nil {
		log.Fatal(err)
	}
	defer res.Body.Close()

	// use "res"
}
