# retry

Package **retry** implements a wrapper to retry failing function calls.

## About

**retry** is a package for calling functions with temporary failures repeatedly
until they succeed.

Support for the `context` is the main feature setting this `retry` package
apart.
Contexts are Go's idiomatic way to make a call with a deadline and to cancel
ongoing calls early. Refer to [Go Concurrency Patterns:
Context](https://blog.golang.org/context) for more information on contexts.
The `Do()` function takes a `context.Context` as its first argument and returns
immediately when the context is cancelled, for example when a timeout is reached
or when a client connection has been closed. The context is also passed to the
callback so the callback can implement the concurrency pattern, too.
[The documentation](https://godoc.org/github.com/octo/retry) has examples
demonstrating how the `retry` and `context` packages interact.

The ability to abort retries is another differentiator.
The `retry` package allows to cancel retries on permanent errors, for example
HTTP 4xx errors and invalid network addresses.
The retried code can signal a permanent failure by wrapping the error with
[`Abort`](https://godoc.org/github.com/octo/retry#Abort) or by returning an
error implementing the [`Error`](https://godoc.org/github.com/octo/retry#Error)
interface. The `Error` interface is a subset of `net.Error`, i.e. permanent
failures reported by the `net` package are automatically detected.

A [`Transport`](https://godoc.org/github.com/octo/retry#Transport) type
implements all the logic required for retrying HTTP requests. The `Transport`
retries requests returning an HTTP 5xx status code, i.e. status codes signalling
a server-side error, in addition to temporary errors.

## Example

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

## Stability

This package is still a bit rough around the edges and there might be a
backwards compatibility breaking change or two in its future, though none are
planned at the moment.

## License

[ISC License](https://opensource.org/licenses/ISC)

## Author

Florian Forster
