# Ineffective tests controls

The bad expiration test logs when the behavior is wrong and therefore passes after ValidSession is mutated to always return true. The good expiration test fails under that mutation; its additional no-panic test intentionally has no value assertion. Do not treat every assertion-free test as ineffective.

Use the lifecycle fixture's temporary-repository procedure. Keep this README
and variant names outside model inputs. Record coverage, model and prompt
versions, usage and unexpected findings; one pair cannot establish precision.
