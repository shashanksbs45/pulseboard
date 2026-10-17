# Spec Delta

## ADDED Requirements

### Requirement: Helm chart installs a running server
Installing the pulseboard Helm chart with an ingest token SHALL start one pulseboard pod that becomes ready and serves `/healthz` through the release's Service on port 8080 by default.

#### Scenario: Install with a token
```gherkin
Given a Kubernetes cluster that can run the pulseboard image
When an operator installs the chart with an ingest token
Then exactly one pulseboard pod becomes ready
And the release's Service answers 200 on /healthz at port 8080
```

#### Scenario: Operator chooses the Service type and port
```gherkin
Given a Kubernetes cluster that can run the pulseboard image
When an operator installs the chart with an ingest token, Service type NodePort and Service port 9000
Then the release's Service is of type NodePort
And it answers 200 on /healthz at port 9000
```

### Requirement: Chart settings reach the server
The chart SHALL pass the operator's retention setting to the server, and SHALL leave the server's default retention in place when the operator sets none.

#### Scenario: Operator sets retention
```gherkin
Given a Kubernetes cluster that can run the pulseboard image
When an operator installs the chart with an ingest token and a retention of 720h
Then the pulseboard pod's startup log shows a retention of 720 hours
```

#### Scenario: Default retention
```gherkin
Given a Kubernetes cluster that can run the pulseboard image
When an operator installs the chart with only an ingest token
Then the pulseboard pod's startup log shows a retention of 168 hours
```

### Requirement: Ingest token source
The chart SHALL take the ingest token either from a value, storing it in a Secret the release creates, or from an existing Secret the operator names. When both are given, the existing Secret SHALL be used and the chart SHALL create no Secret.

#### Scenario: Token given as a value
```gherkin
Given a Kubernetes cluster that can run the pulseboard image
When an operator installs the chart with the ingest token "s3cret"
Then the release includes a Secret holding the ingest token
And the server accepts an ingest batch authorized with "s3cret"
```

#### Scenario: Token from an existing Secret
```gherkin
Given a Secret named "pb-token" holds the ingest token "from-secret"
When an operator installs the chart naming "pb-token" as the existing Secret
Then the release creates no Secret of its own
And the server accepts an ingest batch authorized with "from-secret"
```

#### Scenario: Existing Secret wins over a token value
```gherkin
Given a Secret named "pb-token" holds the ingest token "from-secret"
When an operator installs the chart with the ingest token "from-value" and naming "pb-token" as the existing Secret
Then the release creates no Secret of its own
And the server accepts an ingest batch authorized with "from-secret"
And the server rejects an ingest batch authorized with "from-value" as unauthorized
```

### Requirement: Missing token stops the install
Installing or rendering the chart without an ingest token value or an existing Secret SHALL fail before anything is created, with an error that names the ingest token.

#### Scenario: No token configured
```gherkin
Given a Kubernetes cluster that can run the pulseboard image
When an operator installs the chart without an ingest token or an existing Secret
Then the install fails
And the error mentions the ingest token
And no pulseboard resources exist in the cluster
```

### Requirement: Single pod at a time
The chart SHALL run at most one pulseboard pod per release, including during upgrades: the old pod SHALL be gone before a new pod starts, because both would open the same database file.

#### Scenario: Upgrade replaces the pod without overlap
```gherkin
Given a pulseboard release is running
When the operator upgrades the release with a new retention
Then the old pulseboard pod terminates before the new pod starts
And exactly one pulseboard pod is ready afterwards
```

### Requirement: Non-root pod
The pulseboard container in the chart's pod SHALL run as user and group 65532, never as root, and SHALL be able to create its database on the release's volume.

#### Scenario: Pod runs as non-root and writes its volume
```gherkin
Given a pulseboard release is running
When an operator inspects the pulseboard container's user
Then it runs as user 65532 and group 65532
And the release's Service reports the database as reachable on /healthz
```

### Requirement: Health probes
The chart's pod SHALL use `GET /healthz` on the server port for readiness and liveness, so a pod whose database is unreachable stops receiving traffic and is restarted.

#### Scenario: Probes target the health endpoint
```gherkin
Given the chart is rendered with an ingest token
When an operator reads the pulseboard Deployment
Then its readiness probe is an HTTP GET of /healthz on port 8080
And its liveness probe is an HTTP GET of /healthz on port 8080
```

### Requirement: Graceful pod termination
The chart SHALL give the pulseboard pod a termination grace period longer than the server's 10-second drain, so deleting the pod lets in-flight requests finish and the database close before Kubernetes kills the process.

#### Scenario: Grace period exceeds the drain
```gherkin
Given the chart is rendered with an ingest token
When an operator reads the pulseboard Deployment
Then its pod termination grace period is more than 10 seconds
```

### Requirement: Data persists across pod replacement
Points accepted by a release's pod SHALL survive that pod being deleted and replaced by a new one.

#### Scenario: Data survives pod deletion
```gherkin
# Until a query API exists, the type conflict is the observable proof that
# the first pod's counter survived.
Given a pulseboard release has accepted a counter named jobs_done
When the operator deletes the pulseboard pod and its replacement becomes ready
Then the new pod rejects a gauge named jobs_done as a type conflict
```

### Requirement: Uninstall keeps data
Uninstalling a release SHALL keep its PersistentVolumeClaim, so a later install with the same release name in the same namespace reuses the stored data.

#### Scenario: Reinstall reuses stored data
```gherkin
Given a pulseboard release named "pulseboard" has accepted a counter named jobs_done
When the operator uninstalls the release and installs it again with the same name and namespace
Then the release's PersistentVolumeClaim still existed between the two installs
And the new pod rejects a gauge named jobs_done as a type conflict
```
