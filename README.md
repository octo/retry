# retry

Package **retry** implements a wrapper to retry failing function calls.

## About

**retry** is a package for calling functions with temporary failures repeatedly
until they succeed.

The main difference to other retry packages is support for the `context`
package. Retries are aborted immediately when the context is cancelled, e.g.
when a timeout is reached or a client connection has been closed. The godoc
documentation has examples demonstrating how the `retry` and `context` packages
interact.

## License

[ISC License](https://opensource.org/licenses/ISC)

## Author

Florian Forster
