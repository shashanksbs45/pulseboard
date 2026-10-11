# Spec Delta

## Purpose

Defines the HTTP contract applications use to push gauge and counter metric points into pulseboard, including authentication, payload format, validation rules, limits, and per-point results.

## ADDED Requirements

### Requirement: Ingest endpoint authentication
`POST /api/v1/ingest` SHALL require an `Authorization: Bearer <token>` header matching the configured ingest token, and SHALL respond HTTP 401 without storing anything when the header is missing or the token does not match.

#### Scenario: Missing token
```gherkin
Given a valid batch of points
When a client posts the batch without an Authorization header
Then the response status is 401
And no points are stored
```

#### Scenario: Wrong token
```gherkin
Given a valid batch of points
When a client posts the batch with "Authorization: Bearer wrong"
Then the response status is 401
And no points are stored
```

### Requirement: Batch payload format
The request body SHALL be a JSON array of point objects. Each point has `name` (string), `type` (`"gauge"` or `"counter"`), `value` (number), optional `labels` (object of string to string), and optional `ts` (integer Unix time in milliseconds).

#### Scenario: Valid batch accepted
```gherkin
When an authenticated client posts the batch:
  """json
  [{"name":"queue_depth","type":"gauge","value":42,"labels":{"host":"pi-1"},"ts":1759480000000}]
  """
Then the response status is 200
And the response body is:
  """json
  {"accepted":1,"rejected":[]}
  """
```

#### Scenario: Missing timestamp uses receive time
```gherkin
Given the server clock reads 1759480000000 ms
When an authenticated client posts a point without "ts"
Then the point is accepted
And it is stored with timestamp 1759480000000
```

### Requirement: Request-level errors
The system SHALL reject the whole request with no points stored when the body is not a JSON array of objects (HTTP 400), is an empty array (HTTP 400), contains more than 5,000 points (HTTP 413), or exceeds 5 MiB (HTTP 413).

#### Scenario: Malformed JSON
```gherkin
When an authenticated client posts the body "{not json"
Then the response status is 400
And no points are stored
```

#### Scenario: Batch too large
```gherkin
When an authenticated client posts an array of 5,001 valid points
Then the response status is 413
And no points are stored
```

### Requirement: Per-point validation
Each point SHALL be validated on its own. A point is invalid if its name does not match `^[a-zA-Z_][a-zA-Z0-9_]*$` or exceeds 200 characters, its type is not `gauge` or `counter`, its value is missing or not a number, or it is a counter with a negative value.

#### Scenario: Invalid metric name
```gherkin
When an authenticated client posts a batch containing a point named "2xx-rate"
Then that point is rejected with reason "invalid_name"
```

#### Scenario: Unknown type
```gherkin
When an authenticated client posts a batch containing a point of type "histogram"
Then that point is rejected with reason "invalid_type"
```

#### Scenario: Negative counter
```gherkin
When an authenticated client posts a batch containing a counter point with value -1
Then that point is rejected with reason "invalid_value"
```

### Requirement: Label limits
A point SHALL have at most 10 labels. Label keys MUST match `^[a-zA-Z_][a-zA-Z0-9_]*$`, and label values MUST be non-empty strings of at most 128 characters. A point that breaks any of these rules is rejected.

#### Scenario: Too many labels
```gherkin
When an authenticated client posts a batch containing a point with 11 labels
Then that point is rejected with reason "too_many_labels"
```

#### Scenario: Label value too long
```gherkin
When an authenticated client posts a batch containing a point with a 129-character label value
Then that point is rejected with reason "invalid_label"
```

### Requirement: Timestamp bounds
A point's timestamp SHALL be rejected when it is more than 10 minutes ahead of the server clock or older than the configured retention period.

#### Scenario: Future timestamp
```gherkin
When an authenticated client posts a point whose "ts" is 1 hour ahead of the server clock
Then that point is rejected with reason "timestamp_out_of_range"
```

#### Scenario: Expired timestamp
```gherkin
Given retention is 7 days
When an authenticated client posts a point whose "ts" is 8 days in the past
Then that point is rejected with reason "timestamp_out_of_range"
```

### Requirement: Partial success response
Valid points SHALL be stored even when other points in the same batch are rejected. The response body SHALL be `{"accepted":<count>,"rejected":[{"index":<i>,"reason":<code>}]}`, where `index` is the zero-based position in the batch. The status SHALL be 200 if all points are accepted, 207 if some are rejected, and 422 if all are rejected.

#### Scenario: Mixed batch
```gherkin
Given a batch of 3 points where only the point at index 1 has an invalid name
When an authenticated client posts the batch
Then the response status is 207
And the response body is:
  """json
  {"accepted":2,"rejected":[{"index":1,"reason":"invalid_name"}]}
  """
And the 2 valid points are stored
```

#### Scenario: All points rejected
```gherkin
Given a batch in which every point is invalid
When an authenticated client posts the batch
Then the response status is 422
And no points are stored
```

### Requirement: Stable metric type
A metric name SHALL keep the type it first had while any of its points are stored. A point whose type differs from the stored type, or from an earlier accepted point for the same name in the same batch, SHALL be rejected with reason `type_conflict`.

#### Scenario: Type conflict with stored metric
```gherkin
Given "jobs_done" is stored as a counter
When an authenticated client posts a gauge point named "jobs_done"
Then that point is rejected with reason "type_conflict"
```

#### Scenario: Type conflict within a batch
```gherkin
Given no metric named "new_metric" is stored
When an authenticated client posts a batch with a counter "new_metric" at index 0 and a gauge "new_metric" at index 1
Then the point at index 0 is accepted
And the point at index 1 is rejected with reason "type_conflict"
```

### Requirement: Active series limit
A series is a metric name plus its exact label set. The system SHALL hold at most 10,000 active series, where a series is active while it has stored points. A point that would create a new series beyond the limit SHALL be rejected with reason `series_limit_exceeded`, and points for existing series SHALL still be accepted.

#### Scenario: New series over limit
```gherkin
Given 10,000 series are active
When an authenticated client posts one point for an existing series and one point for a new series
Then the existing-series point is accepted
And the new-series point is rejected with reason "series_limit_exceeded"
```
