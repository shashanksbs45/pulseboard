# Spec Delta

## Purpose

Defines how accepted metric points are persisted, how series and metric types are tracked, and how long raw data is retained, so data survives restarts and disk use stays bounded.

## ADDED Requirements

### Requirement: Durable writes
Points counted as accepted in an ingest response SHALL be durably stored before that response is sent, and SHALL survive a process restart.

#### Scenario: Data survives restart
- **WHEN** a batch is accepted, the server is stopped, and the server is started again with the same database path
- **THEN** every accepted point is still stored with its name, type, labels, value, and timestamp

### Requirement: Millisecond timestamp precision
The system SHALL store timestamps as Unix time in milliseconds without loss of precision.

#### Scenario: Timestamp round-trip
- **WHEN** a point is accepted with `ts` `1759480000123`
- **THEN** the stored point has timestamp `1759480000123`

### Requirement: Series identity
Two points SHALL belong to the same series if and only if they have the same metric name and the same label set, regardless of the order labels appear in the payload.

#### Scenario: Label order does not matter
- **WHEN** one point has labels `{"a":"1","b":"2"}` and another with the same name has `{"b":"2","a":"1"}`
- **THEN** both points belong to the same series

### Requirement: Duplicate timestamp overwrite
When a series receives a point with the same timestamp as an existing point, the newer value SHALL replace the stored value, so each series holds at most one value per millisecond.

#### Scenario: Re-sent point overwrites
- **WHEN** a series has a value `5` at `ts` T and a point with value `7` at `ts` T is accepted
- **THEN** the series holds exactly one point at T, with value `7`

### Requirement: Retention enforcement
The system SHALL delete points older than the configured retention period (default 7 days). A deletion pass SHALL run at startup and at least once per hour, so no point remains more than 1 hour past its expiry while the server is running.

#### Scenario: Expired points removed
- **WHEN** retention is 7 days and a stored point's timestamp is 7 days and 2 hours old
- **THEN** after the next deletion pass, that point is no longer stored

#### Scenario: Recent points kept
- **WHEN** a deletion pass runs
- **THEN** points younger than the retention period are not deleted

### Requirement: Expired series and types are released
When all points of a series have been deleted, the series SHALL no longer count toward the active series limit. When all points of a metric name have been deleted, its type SHALL no longer be enforced.

#### Scenario: Series slot freed
```gherkin
Given 10,000 series are active, one of which is the only series of "old_metric"
And a retention pass has deleted every point of "old_metric"
When an authenticated client posts a point for a new series
Then that point is accepted
```

#### Scenario: Type can be redefined after expiry
```gherkin
Given "old_metric" was stored as a counter
And a retention pass has deleted every point of "old_metric"
When an authenticated client posts a gauge point named "old_metric"
Then that point is accepted
```
