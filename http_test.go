package retry

import (
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"strings"
	"testing"
)

type testTransport struct {
	status []int
}

func (t *testTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body == nil {
		return nil, errors.New("req.Body == nil")
	}
	defer req.Body.Close()

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
			transport: &testTransport{status: []int{501, 502, 200}},
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

	res, err := c.Get("http://example.com/")
	if err != nil {
		log.Fatal(err)
	}

	if res.StatusCode >= 500 && res.StatusCode < 600 {
		panic("this does not happen. HTTP 5xx errors are reported as errors.")
	}

	if res.StatusCode >= 400 && res.StatusCode < 500 {
		log.Fatalf("user error: %s", res.Status)
	}

}
