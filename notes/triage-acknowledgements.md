# Triage acknowledgement controls

Status: protocol controls pass; follow-up implementation review pending. Engineering
prompt version 7 asks reviewers to return an empty findings array when they have
no alleged problem. Task completion is recorded by the engine.

Triage can identify an original model nit as an assessment acknowledgement by
returning its number, its complete original rationale, and a reason. Accepted
entries remain in review decisions with the original finding and triage identity.
The accounting rejects analyzer entries, suggested patches, higher severities,
missing or altered evidence, duplicate decisions, and verdict or merge conflicts.
An unmentioned claim is restored. There is no title-matching deletion rule.

The model still decides whether the title and rationale allege a problem. An
exact quote binds the decision to the original evidence; it does not prove that
semantic judgment correct. Weak claims, missing tests, and mixed praise/problem
entries must remain findings. These controls do not establish general review
precision or complete the engineering-profile acceptance contract.

The scripted tests exercise actual triage accounting and report rendering.
[Mutation evidence](evidence/triage-acknowledgement-mutations.json) records eleven
injected regressions that failed their guard tests. This measures the engine's
safeguards, not a model's classifications.

To run the opt-in semantic controls with the configured user credential source:

```sh
NITPICK_EVAL_ACKNOWLEDGEMENTS=1 go test -tags=eval ./internal/review \
  -run TestLiveTriageSeparatesAcknowledgementsFromWeakClaims -count=1 -v
```

This makes Qwen3.8-27B calls. It checks nine mixed entries in three orders and
prints the prompt/schema digests, retained findings, and decisions. Normal tests
and eval-tag compilation do not enable these calls. Keep their results separate
from the frozen version-5 fixture evaluation, which predates this change.

The [version-6 live controls](evidence/triage-acknowledgement-v6-trials.json)
passed one input order and failed two, preserving all substantive/protected
entries but retaining acknowledgements. A diagnostic response omitted those
entries from findings and omitted the optional acknowledgement array. Version 7
requires that array and explicitly accounts for every number across the three
decision arrays. All three
[version-7 input orders](evidence/triage-acknowledgement-v7-trials.json) passed,
retaining the six substantive/protected entries and recording three
acknowledgements each. The safeguards are unchanged. These explicit synthetic
controls do not estimate false-negative rates on ambiguous natural findings.

The [implementation review adjudication](engineering-implementation-review.md)
records two fixture-harness findings and their disposition. The subsequent
validation response controls are recorded separately in the
[acceptance checkpoint](engineering-acceptance-audit.md#validation-response-contract).
