# retry

Package **retry** implements a wrapper to retry failing function calls.

## About

**retry** is a package for calling functions with temporary failures repeatedly
until they succeed.

The main difference to other retry packages is support for the `context`
package. Retries are aborted immediately when the context is cancelled, e.g.
when a timeout is reached or a client connection has been closed. [The
documentation](https://godoc.org/github.com/octo/retry) has examples
demonstrating how the `retry` and `context` packages interact.

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
