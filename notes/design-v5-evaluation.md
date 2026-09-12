# Engineering version-5 control evaluation

Decision: needs revision. This is a synthetic mechanism evaluation with
GLM-5.3-Flash review and Qwen3.8-27B triage, not acceptance of the engineering
profile or a full assessment of this repository.

[Retained reports](evidence/design-v5-trials.json) contain all 42 trials: seven
mechanisms, bad and intended-good variants, three repetitions each. All 14
source digests remain identical across repetitions. The
[operator policy](evidence/design-v5-policy.yaml) is retained without credentials.
The run used planner 4, prompt engineering-5, and a binary built from pre-rebase
commit `38233ec`. It disabled linters; boundary enforcement ran separately.

Forty trials completed their selected model assessments. Lifecycle bad trial 2
and compatibility good trial 3 retained findings but returned exit 2 because an
expert severity verdict lacked a usable level. These are incomplete assessments.
The three forbidden-boundary trials returned exit 1 for deterministic violations;
that is a completed check finding a problem.

## What the reports establish

| Mechanism | Bad variant | Intended-good variant |
| --- | --- | --- |
| Ineffective tests | Logging instead of asserting identified in all three | Existing no-panic test respected; missing acceptance/equality cases reported |
| Lifecycle | Uninitialized-map/readiness defect identified in all three; one partial assessment | Two disputed zero-value API claims; supplied consumer constructs correctly |
| Failure propagation | Discarded persistence errors identified in all three | Two acknowledgement entries; one report without findings |
| Accepted boundaries | Forbidden import identified deterministically in all three | Imports comply; storage comment has a separate real mismatch |
| Coupled duplication | Two correct diagnoses; one report reverses which function charges shipping | Four acknowledgement entries across the three reports |
| Indirection | Redundant forwarding chain identified in all three | Five acknowledgement entries across the three reports |
| Compatibility | Version-1 decoding failure identified in all three | Two reports without findings; third reports missing coverage and is partial |

The [manual adjudication](evidence/design-v5-adjudication.json) classifies all
66 final findings individually. Three are deterministic boundary violations.
Twenty are acknowledgements of successful assessment, six depend on unsupported
assumptions, two concern the ambiguous cache contract, and one incorrectly treats
public API documentation as slop. Other entries include seeded mechanisms and
separate valid comment/test gaps. These categories count findings, including
multiple findings about one defect; they are not a precision score.

The reports also retain 27 withheld decisions, which are outside those final
finding counts. No advisory signals were returned. Diagnosis, remedy, category,
and execution completeness remain separate: the last compatibility diagnosis is
correct, but deleting its compatibility comment would not restore old records.
The good compatibility trial's missing-test recommendation also does not justify
classifying an existing test as ineffective slop.

## Follow-up

Version 6 adds explicit acknowledgement decisions and keeps unaccounted claims.
Its scripted safeguards passed ten mutation controls. The first live mixed-entry
control passed one of three input orders; the other two retained acknowledgements.
All six substantive or structurally protected entries survived each order.
The implementation is not qualified yet; a raw response omitted the acknowledgement array. Version 7 requires it,
and all three new input orders pass. This is a protocol control, not a
general precision claim.

Keep the original lifecycle and boundary fixtures and their disagreements. Do
not relabel an ambiguous API or a misleading comment as a clean repository.
Prompt and model roles changed from the retired version-4 evaluation, so these
results cannot isolate either change's effect. Full internal assessment and
required-model adoption remain outstanding.
