# Spec Delta

## Purpose

Defines how pulseboard runs as a container image, so operators can start, configure, persist and stop it with standard container tooling and rely on the same behavior across image rebuilds.

## ADDED Requirements

### Requirement: Default container command
Running the image with no command SHALL start `pulseboard serve`, listening on port 8080 inside the container and storing its database at `/data/pulseboard.db`.

#### Scenario: Container starts with defaults
```gherkin
Given the pulseboard image is built
When an operator runs the image with only an ingest token configured
Then the server answers 200 on /healthz at container port 8080
And its database file is /data/pulseboard.db
```

### Requirement: Container settings
The container SHALL accept the same environment variables and `serve` flags as the binary. A setting the operator supplies SHALL take precedence over the image's default.

#### Scenario: Operator overrides the listen address
```gherkin
Given the pulseboard image is built
When an operator runs the image with an ingest token and PULSEBOARD_ADDR set to ":9090"
Then the server answers 200 on /healthz at container port 9090
```

#### Scenario: Operator passes serve flags
```gherkin
Given the pulseboard image is built
When an operator runs the image with an ingest token and the command "serve --retention 24h"
Then the server starts with a retention of 24 hours
```

### Requirement: Missing token stops the container
A container started without an ingest token SHALL exit with a non-zero status and log an error naming the ingest token.

#### Scenario: No ingest token
```gherkin
Given the pulseboard image is built
When an operator runs the image without an ingest token
Then the container exits with a non-zero status
And its log mentions the ingest token
```

### Requirement: Non-root process
The server process in the container SHALL run as the unprivileged user and group 65532, never as root.

#### Scenario: Server runs as non-root
```gherkin
Given a pulseboard container is running
When an operator lists the container's processes
Then the pulseboard process runs as user 65532
```

### Requirement: Data persists on the /data volume
Points accepted by a container SHALL survive replacing that container with a new one that mounts the same volume at `/data`.

#### Scenario: Data survives container replacement
```gherkin
Given a pulseboard container with a named volume mounted at /data has accepted a batch of points
When the operator removes that container and starts a new one with the same volume
Then the new container reports the database as reachable on /healthz
And every point the first container accepted is still stored
```

### Requirement: Clean stop
`docker stop` SHALL shut the server down gracefully: it SHALL stop accepting requests, close the database, and exit with status 0.

#### Scenario: Idle container stops cleanly
```gherkin
Given a pulseboard container is running with no requests in flight
When the operator stops it with docker stop
Then the container exits with status 0 before the stop timeout
```
