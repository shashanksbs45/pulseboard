# Spec Delta

## MODIFIED Requirements

### Requirement: Configuration via flags and environment
The system SHALL accept each setting as a command-line flag or an environment variable: listen address (`--addr` / `PULSEBOARD_ADDR`), database path (`--db` / `PULSEBOARD_DB`), ingest token (`--ingest-token` / `PULSEBOARD_INGEST_TOKEN`), read token (`--read-token` / `PULSEBOARD_READ_TOKEN`, optional, default empty), and retention period (`--retention` / `PULSEBOARD_RETENTION`, default `168h`). A flag SHALL take precedence over its environment variable.

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
When the operator runs "pulseboard serve" with an ingest token
Then the server starts
And read endpoints answer without credentials
```
