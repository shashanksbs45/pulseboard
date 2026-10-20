# Spec Delta

## Purpose

Defines the read side of pulseboard's HTTP API: discovering stored metrics, labels and series, and reading a metric's points over a time range, raw or bucketed, with pagination, read authentication and validation of every parameter.

## ADDED Requirements

### Requirement: Read authentication
When a read token is configured, every read endpoint (`/api/v1/metrics`, `/api/v1/metrics/{name}/labels`, `/api/v1/metrics/{name}/series` and `/api/v1/query`) SHALL require `Authorization: Bearer <read token>` and SHALL respond 401 with error code `unauthorized` otherwise. The ingest token SHALL NOT authorize reads. When no read token is configured, read endpoints SHALL answer without credentials.

#### Scenario: Read token configured and missing from the request
```gherkin
Given the server has the read token "r3ad"
When a client lists metrics without an Authorization header
Then the response status is 401
And the error code is "unauthorized"
```

#### Scenario: Read token configured and supplied
```gherkin
Given the server has the read token "r3ad"
When a client lists metrics with "Authorization: Bearer r3ad"
Then the response status is 200
```

#### Scenario: Ingest token does not authorize reads
```gherkin
Given the server has the ingest token "1ngest" and the read token "r3ad"
When a client lists metrics with "Authorization: Bearer 1ngest"
Then the response status is 401
```

#### Scenario: No read token configured
```gherkin
Given the server has no read token
When a client lists metrics without an Authorization header
Then the response status is 200
```

### Requirement: List metrics
`GET /api/v1/metrics` SHALL return every metric that has stored points as `{"metrics":[{"name":<name>,"type":<type>}]}`, sorted by name. It SHALL return an empty list when nothing is stored.

#### Scenario: Two metrics stored
```gherkin
Given a gauge "queue_depth" and a counter "jobs_done" have stored points
When a client lists metrics
Then the response status is 200
And the response body is:
  """json
  {"metrics":[{"name":"jobs_done","type":"counter"},{"name":"queue_depth","type":"gauge"}]}
  """
```

#### Scenario: Nothing stored
```gherkin
Given no points are stored
When a client lists metrics
Then the response status is 200
And the response body is:
  """json
  {"metrics":[]}
  """
```

### Requirement: List a metric's labels
`GET /api/v1/metrics/{name}/labels` SHALL return every label key used by the metric's series, each with its distinct values, as `{"labels":{<key>:[<value>,...]}}`. Values SHALL be sorted and keys SHALL appear in sorted order. A metric whose series have no labels SHALL return `{"labels":{}}`.

#### Scenario: Labels across several series
```gherkin
Given "cpu_usage" has series with labels {"host":"nas","core":"0"}, {"host":"nas","core":"1"} and {"host":"pi"}
When a client lists the labels of "cpu_usage"
Then the response status is 200
And the response body is:
  """json
  {"labels":{"core":["0","1"],"host":["nas","pi"]}}
  """
```

### Requirement: List a metric's series
`GET /api/v1/metrics/{name}/series` SHALL return every series of the metric as `{"series":[{"labels":{...}}]}`, one entry per distinct label set, in a stable order.

#### Scenario: Two series
```gherkin
Given "cpu_usage" has series with labels {"host":"nas"} and {"host":"pi"}
When a client lists the series of "cpu_usage"
Then the response status is 200
And the response lists exactly the label sets {"host":"nas"} and {"host":"pi"}
```

### Requirement: Unknown metric on discovery
The per-metric discovery endpoints (`/labels` and `/series`) SHALL respond 404 with error code `unknown_metric` when the metric name is well formed but has no stored points, and 400 with `invalid_param` when the name is not a valid metric name.

#### Scenario: Metric not stored
```gherkin
Given no metric named "cpu_usag" is stored
When a client lists the series of "cpu_usag"
Then the response status is 404
And the error code is "unknown_metric"
```

#### Scenario: Malformed metric name
```gherkin
When a client lists the labels of "cpu-usage"
Then the response status is 400
And the error code is "invalid_param"
```

### Requirement: Query series selection
`GET /api/v1/query` SHALL require a `name` parameter and SHALL accept repeated `label=<key>:<value>` filters, split at the first colon. A series SHALL match when it belongs to the named metric and carries every filtered label with exactly that value. Other labels on the series SHALL NOT prevent a match.

#### Scenario: Subset label match
```gherkin
Given "cpu_usage" has series with labels {"host":"nas","core":"0"}, {"host":"nas","core":"1"} and {"host":"pi","core":"0"}
When a client queries "cpu_usage" with the filter "host:nas"
Then the response contains exactly the series {"host":"nas","core":"0"} and {"host":"nas","core":"1"}
```

#### Scenario: Label value containing a colon
```gherkin
Given "http_requests" has a series with labels {"url":"http://nas/api"}
When a client queries "http_requests" with the filter "url:http://nas/api"
Then the response contains that series
```

### Requirement: Query time range
`start` and `end` SHALL be integer Unix times in milliseconds, and the range SHALL include both ends. When `end` is omitted it SHALL be the server's current time. When `start` is omitted it SHALL be one hour before `end`.

#### Scenario: Default range is the last hour
```gherkin
Given the server clock reads 1759480000000 ms
And "queue_depth" has points at 1759476300000, 1759476400000 and 1759480000000
When a client queries "queue_depth" without start or end
Then the response contains the points at 1759476400000 and 1759480000000
And it does not contain the point at 1759476300000
```

#### Scenario: Both ends are included
```gherkin
Given "queue_depth" has points at 1000, 2000 and 3000
When a client queries "queue_depth" with start 1000 and end 3000
Then the response contains all three points
```

### Requirement: Raw query response
Without `step` and `agg`, `/query` SHALL return the stored points unchanged as `{"name":<name>,"type":<type>,"series":[{"labels":{...},"points":[[<ts>,<value>],...]}],"next_cursor":<cursor or null>}`. Points within a series SHALL be in ascending time order.

#### Scenario: Raw points for one series
```gherkin
Given the gauge "queue_depth" with labels {"host":"pi"} has values 4 at 1000 and 7 at 2000
When a client queries "queue_depth" with start 0 and end 5000
Then the response status is 200
And the response body is:
  """json
  {"name":"queue_depth","type":"gauge","series":[{"labels":{"host":"pi"},"points":[[1000,4],[2000,7]]}],"next_cursor":null}
  """
```

### Requirement: Query matching nothing
A well-formed `/query` that matches no points SHALL respond 200 with an empty `series` list, whether the metric is unknown, no series matches the label filters, or the range holds no points. When the metric is unknown, `type` SHALL be `null`.

#### Scenario: Unknown metric
```gherkin
Given no metric named "cpu_usag" is stored
When a client queries "cpu_usag"
Then the response status is 200
And the response body is:
  """json
  {"name":"cpu_usag","type":null,"series":[],"next_cursor":null}
  """
```

#### Scenario: Label filter matches no series
```gherkin
Given "cpu_usage" has a series with labels {"host":"nas"}
When a client queries "cpu_usage" with the filter "host:router"
Then the response status is 200
And the response contains no series
```

### Requirement: Bucketed query
When both `step` (milliseconds, at least 1000) and `agg` are given, `/query` SHALL return one point per non-empty bucket per series instead of raw points. Buckets SHALL start at multiples of `step` since the Unix epoch, each point's timestamp SHALL be its bucket's start, and only points inside the query range SHALL count toward a bucket. Buckets with no points SHALL be omitted.

#### Scenario: Points grouped into epoch-aligned buckets
```gherkin
Given the gauge "queue_depth" has values 2 at 61000, 4 at 119000 and 9 at 185000
When a client queries "queue_depth" with start 0, end 200000, step 60000 and agg "avg"
Then the series points are [[60000,3],[180000,9]]
```

#### Scenario: Partial first bucket counts only points in range
```gherkin
Given the gauge "queue_depth" has values 2 at 61000 and 4 at 70000
When a client queries "queue_depth" with start 65000, end 119999, step 60000 and agg "max"
Then the series points are [[60000,4]]
```

### Requirement: Gauge aggregations
For a gauge, `agg` SHALL be one of `avg`, `min`, `max`, `sum` or `last`, computed over the points in each bucket. `last` SHALL be the value of the bucket's latest point.

#### Scenario: Each gauge aggregation
```gherkin
Given the gauge "temp" has values 3 at 1000, 9 at 2000 and 6 at 3000
When a client queries "temp" with start 0, end 59999, step 60000 and agg "<agg>"
Then the series points are [[0,<value>]]

Examples:
  | agg  | value |
  | avg  | 6     |
  | min  | 3     |
  | max  | 9     |
  | sum  | 18    |
  | last | 6     |
```

### Requirement: Counter semantics in queries
For a counter, `agg` SHALL be one of `increase`, `rate`, `last` or `max`. `increase` SHALL be the sum of the differences between each point in the bucket and the series' previous point in the query range, where a value lower than the previous one counts as a reset and contributes the new value. The first point in the range SHALL contribute 0. `rate` SHALL be `increase` divided by `step` in seconds.

#### Scenario: Increase within a bucket
```gherkin
Given the counter "requests_total" has values 100 at 1000, 105 at 2000 and 112 at 3000
When a client queries "requests_total" with start 0, end 59999, step 60000 and agg "increase"
Then the series points are [[0,12]]
```

#### Scenario: Increase across a counter reset
```gherkin
Given the counter "requests_total" has values 100 at 1000, 112 at 2000, 3 at 3000 and 10 at 4000
When a client queries "requests_total" with start 0, end 59999, step 60000 and agg "increase"
Then the series points are [[0,22]]
```

#### Scenario: Increase spans bucket boundaries
```gherkin
Given the counter "requests_total" has values 100 at 50000 and 130 at 70000
When a client queries "requests_total" with start 0, end 119999, step 60000 and agg "increase"
Then the series points are [[0,0],[60000,30]]
```

#### Scenario: Rate is increase per second
```gherkin
Given the counter "requests_total" has values 100 at 1000 and 160 at 2000
When a client queries "requests_total" with start 0, end 59999, step 60000 and agg "rate"
Then the series points are [[0,1]]
```

### Requirement: Aggregation must fit the metric type
When the named metric exists and `agg` is a known aggregation that its type doesn't allow, `/query` SHALL respond 400 with error code `unsupported_agg`. When the metric doesn't exist, a known aggregation SHALL NOT cause an error.

#### Scenario: Averaging a counter
```gherkin
Given "requests_total" is stored as a counter
When a client queries "requests_total" with step 60000 and agg "avg"
Then the response status is 400
And the error code is "unsupported_agg"
```

#### Scenario: Increase on a gauge
```gherkin
Given "queue_depth" is stored as a gauge
When a client queries "queue_depth" with step 60000 and agg "increase"
Then the response status is 400
And the error code is "unsupported_agg"
```

#### Scenario: Known aggregation on an unknown metric
```gherkin
Given no metric named "cpu_usag" is stored
When a client queries "cpu_usag" with step 60000 and agg "avg"
Then the response status is 200
And the response contains no series
```

### Requirement: Query pagination
`/query` SHALL return at most `limit` points per response (raw points or buckets, counted across all series), where `limit` defaults to 10,000 and SHALL NOT exceed 50,000. Results SHALL be ordered by series, then time. When more results remain, `next_cursor` SHALL be a string that returns the next page when passed as `cursor`. On the page holding the last result it SHALL be `null`, including when the total is an exact multiple of `limit`, so a non-null cursor always leads to a non-empty page.

#### Scenario: Walking pages
```gherkin
Given the gauge "queue_depth" has 25 points in the range 0 to 100000
When a client queries "queue_depth" over that range with limit 10
Then the first page holds 10 points and a next cursor
And following the cursor returns a second page of 10 points and a next cursor
And following that cursor returns a final page of 5 points with a null next cursor
And the three pages together hold each of the 25 points exactly once
```

#### Scenario: Total is an exact multiple of the limit
```gherkin
Given the gauge "queue_depth" has 20 points in the range 0 to 100000
When a client queries "queue_depth" over that range with limit 10
Then the first page holds 10 points and a next cursor
And following the cursor returns a second page of 10 points with a null next cursor
```

#### Scenario: Series split across pages
```gherkin
Given "cpu_usage" has series {"host":"nas"} and {"host":"pi"} with 6 points each in range
When a client queries "cpu_usage" with limit 8
Then the first page holds all 6 points of one series and the first 2 points of the other
And the second page holds the remaining 4 points of that other series under the same labels
```

#### Scenario: Limit above the maximum
```gherkin
When a client queries "queue_depth" with limit 50001
Then the response status is 400
And the error code is "limit_too_large"
```

### Requirement: Cursor bound to its query
A cursor SHALL continue only the query that produced it. Later pages SHALL use the first page's resolved time range, including a defaulted `end`. A request whose `name`, label filters, `start`, `end`, `step` or `agg` differ from the cursor's query, or whose cursor is not one the server issued, SHALL get 400 with error code `invalid_cursor`. `limit` MAY change between pages.

#### Scenario: Defaulted range stays fixed across pages
```gherkin
Given a client queried "queue_depth" without start or end and received a next cursor
And new points for "queue_depth" were stored after the first page
When the client follows the cursor without start or end
Then the page holds only points from the first page's time range
```

#### Scenario: Cursor reused with a different filter
```gherkin
Given a client queried "cpu_usage" with the filter "host:nas" and received a next cursor
When the client sends that cursor with the filter "host:pi"
Then the response status is 400
And the error code is "invalid_cursor"
```

#### Scenario: Tampered cursor
```gherkin
When a client queries "cpu_usage" with the cursor "not-a-cursor"
Then the response status is 400
And the error code is "invalid_cursor"
```

### Requirement: Pagination under concurrent writes and retention
Following cursors SHALL never return the same point twice, and SHALL return every point in the range that stays stored for the whole walk, even when points are written or retention deletes points between pages.

#### Scenario: Retention runs between pages
```gherkin
Given "queue_depth" has 20 points in the query range
And a client has received the first page of 10 points with limit 10
When retention deletes points that the client has already received
And the client follows the cursor
Then the second page holds the remaining 10 points
And none of them repeats a point from the first page
And the second page has a null next cursor
```

### Requirement: Opt-in total count
When `count=true` is given on a request without a cursor, the response SHALL include `"total"`: the number of points (or, with `step`, buckets) the whole query matches across all pages. Without `count=true`, `total` SHALL be absent.

#### Scenario: Count on the first page
```gherkin
Given the gauge "queue_depth" has 25 points in the query range
When a client queries "queue_depth" over that range with limit 10 and count true
Then the response includes "total" equal to 25
```

#### Scenario: Count of an empty result
```gherkin
Given no metric named "cpu_usag" is stored
When a client queries "cpu_usag" with count true
Then the response includes "total" equal to 0
```

### Requirement: Malformed query parameters
`/query` SHALL respond 400 with error code `invalid_param` when a parameter can never be valid: a missing or invalid `name`, a malformed or invalid label filter, a repeated label key, more than 10 label filters, a non-integer `start` or `end`, `start` after `end`, a `step` below 1000, `step` without `agg` or `agg` without `step`, an `agg` naming no aggregation, a `limit` below 1, or a `count` other than `true` or `false`.

#### Scenario: Rejected parameters
```gherkin
When a client queries with the parameters "<params>"
Then the response status is 400
And the error code is "invalid_param"

Examples:
  | params                                   |
  | label=host:nas                           |
  | name=cpu-usage                           |
  | name=cpu&label=hostnas                   |
  | name=cpu&label=:nas                      |
  | name=cpu&label=host:                     |
  | name=cpu&label=1host:nas                 |
  | name=cpu&label=host:a&label=host:b       |
  | name=cpu&start=yesterday                 |
  | name=cpu&start=2000&end=1000             |
  | name=cpu&step=500&agg=avg                |
  | name=cpu&step=60000                      |
  | name=cpu&agg=avg                         |
  | name=cpu&step=60000&agg=median           |
  | name=cpu&limit=0                         |
  | name=cpu&count=yes                       |
```

#### Scenario: Too many label filters
```gherkin
When a client queries "cpu_usage" with 11 label filters on distinct keys
Then the response status is 400
And the error code is "invalid_param"
```

### Requirement: Read error body
Every 400, 401 and 404 response from a read endpoint SHALL have a JSON body `{"error":<code>,"message":<text>}`, where `code` is one of `invalid_param`, `invalid_cursor`, `limit_too_large`, `unsupported_agg`, `unknown_metric` or `unauthorized`, and `message` is human-readable text explaining the problem.

#### Scenario: Error body names the problem
```gherkin
When a client queries "cpu_usage" with limit 50001
Then the response body has "error" equal to "limit_too_large"
And its "message" mentions the maximum of 50000
```
