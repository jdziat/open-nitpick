# Lifecycle assessment controls

The bad case has a consumer that writes to an uninitialized global map. The good
case returns an initialized instance whose caller owns its use. The bad case also
contains a misleading readiness comment; the first model trial missed that defect.

To repeat a model trial, copy either directory into a temporary directory, remove
the `.txt` suffixes, initialize a Git repository on `main`, and commit the files.
Add an empty conventional commit on a second branch for commit-range coverage.
Run `nitpick repo-standards -profile engineering -repo <directory> -base main
-config <external-operator-policy> -check -json` with the same operator policy for
both cases. The policy must configure a model; no credentials are stored here.

Compare check states and examined targets as well as findings. The good case is
not a passing control if a model or analyzer did not run. Record model identities,
policy digest, repeated-run counts and any unexpected findings. One paired trial
does not establish design-review precision.
