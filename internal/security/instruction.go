package security

import "strings"

// Instruction is the model-pass prompt for nitpick security / security_scan.
// The eval harness applies the same text when NITPICK_EVAL_SECURITY is on so
// the bake-off measures this persona, not a generic review. Depth variants
// below are for prompt A/B only; the shipped command uses Instruction.
const Instruction = `Report only security findings: injection, authorization flaws, secret handling, crypto misuse, SSRF, path traversal, unsafe deserialization, and similar. Do not report style, maintainability, or slop.`

// InstructionLight is a narrow security persona: clear, exploitable defects only.
const InstructionLight = `Report only clear, exploitable security bugs (injection, auth bypass, secret exposure, path traversal, and similar). Skip speculative, defense-in-depth, or "could be better" notes. Do not report style, maintainability, or slop.`

// InstructionExtreme is an aggressive auditor persona: prefer a borderline
// security finding over silence. Still security-class only.
const InstructionExtreme = `Act as a pedantic security auditor. Report every security concern you can justify: injection, IDOR and other authorization mismatches (auth present but wrong principal), secret handling and logging, crypto misuse (timing, unbound MACs, weak compares), SSRF, path traversal, unsafe deserialization, existence oracles (403 vs 404), accepting expired tokens, and similar. Prefer reporting a borderline security finding over silence. Do not report style, maintainability, concurrency, or slop.`

// InstructionForDepth selects a security persona by depth name.
// Empty or "deep" is the shipped Instruction. Unknown names fall back to deep.
func InstructionForDepth(depth string) string {
	switch strings.ToLower(strings.TrimSpace(depth)) {
	case "light":
		return InstructionLight
	case "extreme":
		return InstructionExtreme
	default:
		return Instruction
	}
}
