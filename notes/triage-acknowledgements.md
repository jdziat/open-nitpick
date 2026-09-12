# Triage acknowledgement controls

Status: implemented locally; live semantic qualification pending. Engineering
prompt version 6 asks reviewers to return an empty findings array when they have
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
[Mutation evidence](evidence/triage-acknowledgement-mutations.json) records ten
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
