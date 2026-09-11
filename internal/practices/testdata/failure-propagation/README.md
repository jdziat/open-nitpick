# Failure propagation controls

Both cases expose `Register`, whose contract returns persistence failures. The bad
helper discards serialization and writer errors, so callers observe success for
unsaved records. The good helper returns the encoder error through the same caller.
A writer returning an error or an unsupported JSON value triggers the difference.

Use the lifecycle control's temporary-repository procedure with both directories.
Require all three source targets to be examined and retain expert uncertainty.
The intended finding names the failed operation and the misleading success path;
a request for additional abstractions is not a detection of this defect.
