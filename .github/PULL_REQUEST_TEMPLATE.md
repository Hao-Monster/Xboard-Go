## Governance metadata

Requirement IDs: <!-- e.g. AUTH-001, AUTH-003; or N/A: reason -->
Work item IDs: <!-- e.g. GOV-001; or N/A: reason -->
Milestone: <!-- exactly M0, M1, M2, M3, or M4 -->
Closes: <!-- e.g. #123; or N/A: reason -->

## Outcome

<!-- Describe the user-observable or governance result. -->

## Verification

<!-- List commands and PASS / FAIL / NOT RUN. Bind accepted evidence to the exact commit. -->

## Risk and rollback

<!-- State affected risks, compatibility exceptions, migrations, and rollback point. -->

## Checklist

- [ ] I updated affected requirement/work-item/decision/risk records.
- [ ] I ran `go run ./cmd/projectctl generate` after governance data changes.
- [ ] I ran `go run ./cmd/projectctl check`.
- [ ] I did not include secrets, production data, raw request/mail bodies, or executable legacy job payloads.
