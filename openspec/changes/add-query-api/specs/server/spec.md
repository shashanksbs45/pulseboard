# Spec Delta

## MODIFIED Requirements

### Requirement: Configuration via flags and environment
The system SHALL accept each setting as a command-line flag or an environment variable: listen address (`--addr` / `PULSEBOARD_ADDR`), database path (`--db` / `PULSEBOARD_DB`), ingest token (`--ingest-token` / `PULSEBOARD_INGEST_TOKEN`), read token (`--read-token` / `PULSEBOARD_READ_TOKEN`, optional, default empty), whether a read token is required (`--require-read-token` / `PULSEBOARD_REQUIRE_READ_TOKEN`, default `false`), and retention period (`--retention` / `PULSEBOARD_RETENTION`, default `168h`). A flag SHALL take precedence over its environment variable.

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

#### Scenario: Read token from the environment
```gherkin
Given PULSEBOARD_READ_TOKEN is set to "r3ad"
When the operator runs "pulseboard serve" with an ingest token
Then read endpoints require "Authorization: Bearer r3ad"
```

#### Scenario: No read token leaves reads open
```gherkin
Given no read token is configured
And a read token is not required
When the operator runs "pulseboard serve" with an ingest token
Then the server starts
And read endpoints answer without credentials
```

### Requirement: Startup validation
The system SHALL refuse to start, exiting non-zero with a message naming the problem, when the ingest token is empty, a read token is required but empty, the retention period is not a positive duration, or the database cannot be opened.

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

#### Scenario: Required read token is missing
```gherkin
Given an ingest token is configured
And PULSEBOARD_REQUIRE_READ_TOKEN is set to "true"
And PULSEBOARD_READ_TOKEN is unset
When the operator runs "pulseboard serve"
Then the process exits with a non-zero status
And the error message mentions the read token
```

#### Scenario: Required read token is empty
```gherkin
Given an ingest token is configured
And PULSEBOARD_REQUIRE_READ_TOKEN is set to "true"
And PULSEBOARD_READ_TOKEN is set to ""
When the operator runs "pulseboard serve"
Then the process exits with a non-zero status
And the error message mentions the read token
```
