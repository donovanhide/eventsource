# Change log

All notable changes to the project will be documented in this file. This project adheres to [Semantic Versioning](http://semver.org).

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
