# Spec Delta

## ADDED Requirements

### Requirement: Read token source
The chart SHALL take an optional read token either from a value, storing it in the Secret the release creates, or from a key in the existing Secret the operator names. When the existing Secret is named, it SHALL be the only source, and a missing read-token key in it SHALL leave reads open rather than fail the pod unless a read token is required. When neither source provides a read token and none is required, the server SHALL run with reads open.

#### Scenario: Read token given as a value
```gherkin
Given a Kubernetes cluster that can run the pulseboard image
When an operator installs the chart with an ingest token and the read token "r3ad"
Then the release's Secret holds the read token
And the server answers a metrics listing authorized with "r3ad"
And the server rejects a metrics listing without credentials as unauthorized
```

#### Scenario: Read token from the existing Secret
```gherkin
Given a Secret named "pb-token" holds the ingest token and the read token "from-secret"
When an operator installs the chart naming "pb-token" as the existing Secret
Then the server answers a metrics listing authorized with "from-secret"
```

#### Scenario: Existing Secret without a read token
```gherkin
Given a Secret named "pb-token" holds only the ingest token
When an operator installs the chart naming "pb-token" as the existing Secret, without requiring a read token
Then exactly one pulseboard pod becomes ready
And the server answers a metrics listing without credentials
```

#### Scenario: No read token configured
```gherkin
Given a Kubernetes cluster that can run the pulseboard image
When an operator installs the chart with only an ingest token
Then the release's Secret holds no read token
And the server answers a metrics listing without credentials
```

### Requirement: Required read token
When the operator sets `requireReadToken` to true, the chart SHALL NOT run pulseboard with reads open. Rendering SHALL fail with an error naming the read token when neither `readToken` nor an existing Secret is given. With an existing Secret, a missing, misspelt or empty read-token key SHALL keep the pod from becoming ready. `requireReadToken` SHALL default to false.

#### Scenario: Required without any read token source
```gherkin
When an operator renders the chart with an ingest token and requireReadToken true but no read token or existing Secret
Then rendering fails
And the error mentions the read token
```

#### Scenario: Misspelt read-token key
```gherkin
Given a Secret named "pb-token" holds the ingest token and the read token under the key "read-token"
When an operator installs the chart naming "pb-token" as the existing Secret, with requireReadToken true and the read-token key "readtoken"
Then no pulseboard pod becomes ready
And the pod's log mentions the read token
```

#### Scenario: Required token found
```gherkin
Given a Secret named "pb-token" holds the ingest token and the read token "from-secret"
When an operator installs the chart naming "pb-token" as the existing Secret, with requireReadToken true
Then exactly one pulseboard pod becomes ready
And the server rejects a metrics listing without credentials as unauthorized
And the server answers a metrics listing authorized with "from-secret"
```
