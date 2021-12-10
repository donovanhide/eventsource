# Contributing to this project
 
## Submitting bug reports and feature requests

The LaunchDarkly SDK team monitors the [issue tracker](https://github.com/launchdarkly/eventsource/issues) in this repository. Bug reports and feature requests specific to this project should be filed in this issue tracker. The SDK team will respond to all newly filed issues within two business days.

Some of this code is used by the LaunchDarkly Go SDK. For issues or requests that are more generally related to the LaunchDarkly Go SDK, rather than specifically for the code in this repository, please use the [`go-server-sdk`](https://github.com/launchdarkly/go-server-sdk) repository.
 
## Submitting pull requests
 
We encourage pull requests and other contributions from the community. Before submitting pull requests, ensure that all temporary or unintended code is removed. Don't worry about adding reviewers to the pull request; the LaunchDarkly SDK team will add themselves. The SDK team will acknowledge all pull requests within two business days.
 
## Build instructions
 
### Prerequisites
 
This project should be built against Go 1.8 or newer.

### Building

To build the project without running any tests:
```
make
```

To run the linter:
```
make lint
```

### Testing
 
To build and run all unit tests:
```
make test
```

To run the standardized contract tests that are run against all LaunchDarkly SSE client implementations:
```
make contract-tests
```
