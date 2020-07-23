# Change log

All notable changes to the project will be documented in this file. This project adheres to [Semantic Versioning](http://semver.org).

## [1.6.1] - 2020-07-23
### Changed:
- `Server.Handler()` now uses the standard `context.Context` mechanism to detect when a request has been cancelled, instead of the deprecated `http.CloseNotifier`.

## [1.6.0] - 2020-07-23
### Added:
- `Server.Unregister` method for removing a `Repository` registration and optionally forcing clients to disconnect.
- `Server.PublishWithAcknowledgement` method for ensuring that an action does not happen until an event has been dispatched.

### Fixed:
- Fixed a race condition in which `Server` might close a channel while another goroutine is trying to write to it. This would happen if you registered a `Repository` that replays events, started a handler, then closed the `Server` while the events were still replaying.
- Improved unit test coverage.

## [1.5.0] - 2020-07-15
### Added:
- `Server.MaxConnTime` is an optional setting to make the `Server` automatically close any stream connection that has stayed open for at least that amount of time. This may be useful in preventing server instances from accumulating too many connections in a load-balanced environment.

## [1.4.3] - 2020-07-07
### Changed:
- The only changes in this release are to the test dependencies, to avoid bringing in unnecessary transitive dependencies such as `go-sdk-common`. Some of the test dependencies are now modules that can only be used in Go 1.13&#43;, which means that the test build for this project only runs in Go 1.13&#43;, but it can still be imported by projects that use older Go versions.

## [1.4.2] - 2020-06-04
### Added:
- Added `go.mod` so this package can be consumed as a module. This does not affect code that is currently consuming it via `go get`, `dep`, or `govendor`.

## [1.4.1] - 2020-03-27
### Fixed:
- An error in the backoff logic added in v1.4.0 could cause a panic after many successive retries, due to the exponential backoff value exceeding `math.MaxInt64` resulting in a negative number being passed to `random.Int63n`.

## [1.4.0] - 2020-03-25
### Added:
- New option `StreamOptionErrorHandler` provides an alternate way to receive errors and control how `Stream` behaves after an error.
- New `Stream` method `Restart()` provides a way to make the stream reconnect at any time even if it has not detected an error, using the same retry semantics (backoff, jitter, etc.) that have already been configured.

## [1.3.0] - 2020-03-24
### Added:
- New option `StreamOptionUseBackoff` allows `Stream` to be configured to use exponential backoff for reconnections. There was existing logic for exponential backoff, but it was not working, so until now the retry delay was always the same; for backward compatibility with that behavior, the default is still to not use backoff.
- The new option `StreamOptionRetryResetInterval` can be used in conjunction with `StreamOptionUseBackoff` to determine when, if ever, the retry delay can be reset to its initial value rather than continuing to decrease.
- New option `StreamOptionUseJitter` tells `Stream` to subtract a pseudo-random amount from the retry delay.
- New option `StreamOptionCanRetryFirstConnection` tells `Stream` that it can retry the initial connection attempt. Previously, a failed initial connection would be considered a permanent failure.

## [1.2.0] - 2019-06-17
### Added:
- `NewDecoderWithOptions` allows creating a `Decoder` with non-default settings; currently the only such setting is `DecoderOptionReadTimeout`. Normally you will not need to create a `Decoder` directly; it is done automatically by `Stream`.
### Fixed:
- Reverted an unintentional change in v1.1.0 to the signature of the exported function `NewDecoder`.

## [1.1.0] - 2018-10-03
### Added:
- It is now possible to specify a read timeout for a stream. If the stream does not receive new data (either events or comments) within this interval, it will emit an error and restart the connection.
- New stream constructor methods `SubscribeWithURL` and `SubscribeWithRequestAndOptions` allow any combination of optional configuration parameters to be specified for a stream: read timeout, initial retry delay, last event ID, HTTP client, logger.

## [1.0.1] - 2018-07-20
### Fixed:
- Avoid trying to to decode non-200 responses which was generating extra error messages.

## [1.0.0] - 2018-06-14
Initial release of this fork.

### Added:
- Added support for Close() method.
