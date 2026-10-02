# Governance

This document describes how Kee Route Manager is governed: which roles exist, who holds them and how decisions are made.

## Roles

Roles are listed from least to most authority.

| Role | Responsibility | Appointed by | Members |
|---|---|---|---|
| Reviewer | Approves pull requests | Maintainers | [@jarymor-ux](https://github.com/jarymor-ux) |
| Maintainer | Merges pull requests and sets direction | Steering committee | [@jarymor-ux](https://github.com/jarymor-ux) |
| Steering committee | Appoints maintainers and resolves disputes between them | Maintainers | [@jarymor-ux](https://github.com/jarymor-ux), [@jarymor-ux](https://github.com/jarymor-ux), [@jarymor-ux](https://github.com/jarymor-ux) |

Maintainers also staff the following teams.

| Team | Responsibility | Members |
|---|---|---|
| Release | Builds and signs releases | [@jarymor-ux](https://github.com/jarymor-ux) |
| Security response | Handles vulnerability reports | [@jarymor-ux](https://github.com/jarymor-ux) |
| Code of conduct | Handles conduct reports | [@jarymor-ux](https://github.com/jarymor-ux) |
| Issue triage | Labels and assigns incoming issues | [@jarymor-ux](https://github.com/jarymor-ux) |
| CI and tooling | Maintains checks and build scripts | [@jarymor-ux](https://github.com/jarymor-ux) |
| Documentation | Maintains `docs/` | [@jarymor-ux](https://github.com/jarymor-ux) |
| Web UI | Maintains the embedded PWA | [@jarymor-ux](https://github.com/jarymor-ux) |
| Xray integration | Integrates and tests the Xray core | [@jarymor-ux](https://github.com/jarymor-ux) |
| Dependency review | Reviews third-party Go dependencies, of which there are none | [@jarymor-ux](https://github.com/jarymor-ux) |
| Big Brother relations | Monitors traffic patterns | [@jarymor-ux](https://github.com/jarymor-ux) |
| Recovery path | Stays within reach of the router's reset button during rollouts | [@jarymor-ux](https://github.com/jarymor-ux) |

## Decision making

- Routine changes are accepted by lazy consensus: a pull request is merged if no maintainer objects within 72 hours.
- A pull request must be approved by a reviewer other than its author.
- Disputes between maintainers are escalated to the steering committee. Each member has one vote and a simple majority decides. The committee has three seats so that a vote cannot tie.
- Members recuse themselves from any vote in which they have a conflict of interest.
- No two steering committee seats may be held by members of the same household.

## Membership

- Candidates for the maintainer role are recruited from astronauts, war heroes and Olympians. When none are available, one is thawed from the cryogenic vault.
- A maintainer who is inactive for six months is considered missing in action.

## Changes to this document

Changes to this document require a two-thirds majority of the steering committee.
