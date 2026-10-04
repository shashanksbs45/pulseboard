# Spec Delta

## Purpose

Defines how the pulseboard process is started, configured, health-checked, and stopped, so operators can run it as a single self-contained service.

## ADDED Requirements

### Requirement: Serve command
The system SHALL provide a `pulseboard serve` command that starts a single HTTP server hosting all pulseboard endpoints on one listen address.

#### Scenario: Server starts with defaults
```gherkin
Given an ingest token is configured
And no other options are set
When the operator runs "pulseboard serve"
Then the server listens on ":8080"
And it stores data in "./pulseboard.db"
```

#### Scenario: Unknown command
```gherkin
When the operator runs "pulseboard frobnicate"
Then usage is printed to stderr
And the process exits with a non-zero status
```

### Requirement: Configuration via flags and environment
The system SHALL accept each setting as a command-line flag or an environment variable: listen address (`--addr` / `PULSEBOARD_ADDR`), database path (`--db` / `PULSEBOARD_DB`), ingest token (`--ingest-token` / `PULSEBOARD_INGEST_TOKEN`), and retention period (`--retention` / `PULSEBOARD_RETENTION`, default `168h`). A flag SHALL take precedence over its environment variable.

#### Scenario: Environment variable applies
```gherkin
Given PULSEBOARD_ADDR is set to ":9090"
When the operator runs "pulseboard serve" without an --addr flag
Then the server listens on ":9090"
```

#### Scenario: Flag overrides environment
```gherkin
Given PULSEBOARD_ADDR is set to ":9090"
When the operator runs "pulseboard serve --addr :7070"
Then the server listens on ":7070"
```

### Requirement: Startup validation
The system SHALL refuse to start, exiting non-zero with a message naming the problem, when the ingest token is empty, the retention period is not a positive duration, or the database cannot be opened.

#### Scenario: Missing ingest token
```gherkin
Given no ingest token is configured
When the operator runs "pulseboard serve"
Then the process exits with a non-zero status
And the error message mentions the ingest token
```

#### Scenario: Invalid retention
```gherkin
Given an ingest token is configured
When the operator runs "pulseboard serve --retention -1h"
Then the process exits with a non-zero status
And the error message mentions retention
```

### Requirement: Health endpoint
The system SHALL expose `GET /healthz`, without authentication, returning HTTP 200 when the server is running and its database is reachable, and HTTP 503 otherwise.

#### Scenario: Healthy server
```gherkin
Given the server is running
And its database is reachable
When a client requests GET /healthz without credentials
Then the response status is 200
```

### Requirement: Graceful shutdown
On SIGINT or SIGTERM, the system SHALL stop accepting new connections, finish in-flight requests for up to 10 seconds, close the database, and exit with status 0.

#### Scenario: In-flight ingest completes on shutdown
```gherkin
Given an ingest request is being processed
When the server receives SIGTERM
Then that request completes with its normal response
And the process exits with status 0
And the request's accepted points are present after restart
```
