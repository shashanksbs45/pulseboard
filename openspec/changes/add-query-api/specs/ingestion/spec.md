# Spec Delta

## ADDED Requirements

### Requirement: Counter semantics
A counter's value SHALL be a running total that only goes up, except when its source resets it to a lower value (for example after a process restart). The system SHALL accept a counter point whose value is lower than the series' previous value, as long as it is not negative, and SHALL treat it as a reset when computing increases.

#### Scenario: Counter reset is accepted
```gherkin
Given the counter "requests_total" has a stored value of 112
When an authenticated client posts a "requests_total" counter point with value 3 and a later timestamp
Then the response status is 200
And the point is accepted
```
