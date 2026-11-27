package wire

import "testing"

// These digests were produced by the code of the v2.10.0 release, not by this
// tree. A record is filed under the hash of its inputs, so a change to what an
// input hashes to orphans every stored record: it is never found again, and the
// review that would have replayed it runs live and pays for the model. That is a
// change to be made on purpose, with a bump of Schema and a note, and never as a
// side effect. If this test fails, the hash changed.
func TestInputHashesAreTheOnesTheLastReleaseProduced(t *testing.T) {
	if Schema != 4 {
		t.Fatalf("Schema is %d: the goldens below are for schema 4, so a bump has to regenerate them on purpose", Schema)
	}
	for name, tc := range map[string]struct {
		in   Inputs
		want string
	}{
		"empty": {Inputs{}, "6ad97faf1c9bae8478086d75e1581b8ed0e9cdcaec5a782ceab0f68dc8eee9ac"},
		"a review": {Inputs{
			Tool: "v2.10.0", Provider: "openrouter", Model: "z-ai/glm-5.3-flash", System: "You review code.",
			Settings: []string{"deep=false", "policy=abc"}, Batches: []string{"batch one", "batch two"},
		}, "3332dd41257d719aad226255637f1a2121f143d4130fb3d0c326df49f1714018"},
		"a short one": {Inputs{
			Tool: "v2.10.0", Provider: "p", Model: "m", System: "s", Settings: []string{"a=b"}, Batches: []string{"x"},
		}, "82f1b510c3fd37a5daf1b412ca3b6a99b3830578549b15c6183ccdd8920b505e"},
		"a setting named like a section": {Inputs{Tool: "t", Settings: []string{"batches"}}, "3f8fee6d6f78b6cbe49ae850c83ec6fd5b036595b40b532dfa821a1788021335"},
	} {
		if got := tc.in.Hash(); got != tc.want {
			t.Errorf("%s: hash = %s, want %s (the v2.10.0 release's)", name, got, tc.want)
		}
	}
}
