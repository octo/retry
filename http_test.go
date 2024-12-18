package retry

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
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

	payload, err := io.ReadAll(req.Body)
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

// testResponseWriter writes a response to a bytes.Buffer, i.e. to memory.
// It implements the http.ResponseWriter interface.
type testResponseWriter struct {
	header http.Header
	buffer bytes.Buffer
	status int
}

func (w *testResponseWriter) Header() http.Header {
	return w.header
}

func (w *testResponseWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.buffer.Write(data)
}

func (w *testResponseWriter) WriteHeader(s int) {
	if w.status != 0 {
		panic(fmt.Sprintf("w.status = %d, want 0", w.status))
	}

	w.status = s
}

// testBudgetTransport is an http.RoundTripper that simply calls an http.Handler.
type testBudgetTransport struct {
	http.Handler
}

func (t *testBudgetTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	w := &testResponseWriter{
		header: make(http.Header),
	}

	t.Handler.ServeHTTP(w, req)

	return &http.Response{
		Status:        http.StatusText(w.status),
		StatusCode:    w.status,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        w.header,
		Body:          io.NopCloser(&w.buffer),
		ContentLength: int64(w.buffer.Len()),
		Request:       req,
	}, nil
}

// testHandler is a handler returning success for every other request and 503
// for the remaining requests.
type testHandler struct {
	sync.Mutex
	totalCount int
}

func (h *testHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	h.Lock()
	defer h.Unlock()

	h.totalCount++

	// 50% of requests are "failures". Do blocks of two, because the
	// requests alternate between initial requests and retries and this way
	// we get all retry/failure combinations.
	if h.totalCount%2 == 1 {
		http.Error(w, "Service Unavailable", http.StatusServiceUnavailable)
		return
	}

	fmt.Fprintln(w, "Ok")
}

func TestBudgetHandler(t *testing.T) {
	if os.Getenv("RETRY_ENDTOEND") == "" {
		t.Skip("set the \"RETRY_ENDTOEND\" environment variable to enable this test")
	}

	// testBudgetHandler always produces 25% retries.
	// max 24% retries -> in overload -> status 429
	testBudgetHandler(t, 0.24, 429)
	// max 26% retries -> not overloaded -> status 503
	testBudgetHandler(t, 0.26, 503)
}

func testBudgetHandler(t *testing.T, ratio float64, wantStatus int) {
	t.Helper()

	ticker := time.NewTicker(time.Second / 100)
	wg := &sync.WaitGroup{}
	hndl := &testHandler{}
	client := &http.Client{
		Transport: &testBudgetTransport{
			Handler: &BudgetHandler{
				Handler: hndl,
				Budget: Budget{
					Ratio: ratio,
				},
			},
		},
	}

	responsesByStatus := map[int]int{}
	responsesByStatusLock := &sync.Mutex{}

	for i := 0; i < 200; i++ {
		<-ticker.C

		wg.Add(1)
		go func(n int) {
			defer wg.Done()

			req, err := http.NewRequest(http.MethodGet, "http://example.com/", nil)
			if err != nil {
				t.Errorf("http.NewRequest() = %v", err)
				return
			}
			if n%4 == 0 {
				req.Header.Set("Retry-Attempt", "1")
			}

			res, err := client.Do(req)
			if err != nil {
				t.Errorf("client.Get() = %v", err)
				return
			}

			// avoid start-up effects by only accounting the second
			// half of requests.
			if n < 100 {
				return
			}

			responsesByStatusLock.Lock()
			defer responsesByStatusLock.Unlock()

			responsesByStatus[res.StatusCode]++
		}(i)
	}

	wg.Wait()

	for status, count := range responsesByStatus {
		t.Logf("HTTP %d: %d responses", status, count)
	}

	if got, want := responsesByStatus[200], 50; got != want {
		t.Errorf("responsesByStatus[200] = %d, want %d", got, want)
	}

	if got, want := responsesByStatus[429]+responsesByStatus[503], 50; got != want {
		t.Errorf("responsesByStatus[429] + responsesByStatus[503] = %d, want %d", got, want)
	}

	if got, want := responsesByStatus[wantStatus], 50; got != want {
		t.Errorf("responsesByStatus[%d] = %d, want %d", wantStatus, got, want)
	}
}
