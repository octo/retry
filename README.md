# retry

Package **retry** implements a wrapper to retry failing function calls.

## About

**retry** is a package for calling functions repeatedly until they either
succeed or the action is cancelled, for example due to a timeout. Retrying
operations is a common strategy to deal with temporary failures in distributed
systems, for example when using Remote Procedure Calls (RPCs).

This package provides all features needed in a large scale distributed
environment and does so with a small and idiomatic API surface.
Highlights are:

### SRE best practices

Features and defaults in this package are heavily influenced by the [SRE
book](https://landing.google.com/sre/book.html), particularly the chapters
[Handling
Overload](https://landing.google.com/sre/book/chapters/handling-overload.html)
and [Addressing Cascading
Failures](https://landing.google.com/sre/book/chapters/addressing-cascading-failures.html).
By default this package uses
[jitter](https://godoc.org/github.com/octo/retry#Jitter) to evenly distribute
retries over the retry period and limits the number of
[attempts](https://godoc.org/github.com/octo/retry#Attempts) per request. A
[retry budget](https://godoc.org/github.com/octo/retry#Budget) optionally limits
the number of retries sent to a backend to prevent overload.

### context aware

*retry* supports Go's [`context`](https://golang.org/pkg/context/) package.
Contexts are the idiomatic way to make a call with a deadline and to cancel
ongoing calls early. Refer to [Go Concurrency Patterns:
Context](https://blog.golang.org/context) for more information on contexts.

The `Do()` function takes a `context.Context` as its first argument and returns
immediately when the context is cancelled, for example when a timeout is reached
or when a client connection has been closed. The context is also passed to the
callback, so the callback can also implement the concurrency pattern.
[The documentation](https://godoc.org/github.com/octo/retry) has examples
demonstrating how the `retry` and `context` packages interact.

### Permanent failures

If the client code detects a permanent failure, for example an "access
forbidden" error, it can abort, preventing additional retries. This returns the
(inevitable) error right away instead of wasting additional time and resources
on retrying needlessly.

User code can signal a permanent failure by wrapping the error with
[`Abort`](https://godoc.org/github.com/octo/retry#Abort) or by returning an
error implementing the [`Error`](https://godoc.org/github.com/octo/retry#Error)
interface. The `Error` interface is a subset of `net.Error`, i.e. errors created
by the `net` package will automatically do the right thing.

### HTTP transport

A [`Transport`](https://godoc.org/github.com/octo/retry#Transport) type
implements all the logic required for retrying HTTP requests. The `Transport`
retries requests returning an HTTP 5xx status code, i.e. status codes signalling
a server-side error, in addition to temporary errors.

## Examples

### Cancel retries after timeout

This example, which is taken from [the
documentation](https://godoc.org/github.com/octo/retry), demonstrates how
retries can be cancelled after 10 seconds using the `context` package.

```go
// Create a context which is cancelled after 10 seconds.
ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
defer cancel()

// cb is a function that may or may not fail.
cb := func(_ context.Context) error {
	return nil // or error
}

// Call cb via Do() until it succeeds or the 10 second timeout is reached.
if err := retry.Do(ctx, cb); err != nil {
	log.Printf("cb() = %v", err)
}
```

### Retry HTTP 5xx errors

This example, which is taken from [the
documentation](https://godoc.org/github.com/octo/retry), demonstrates how
to retry an HTTP POST request until it succeeds or the 30 second timeout is
reached.

```go
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()

c := &http.Client{
	Transport: &Transport{},
}

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
// ...
```

## Stability

This package is still a bit rough around the edges and there might be a
backwards compatibility breaking change or two in its future, though none are
planned at the moment.

## License

[ISC License](https://opensource.org/licenses/ISC)

## Author

Florian Forster
