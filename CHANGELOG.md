# Changelog

## [1.3.0](https://github.com/jdziat/open-nitpick/compare/v1.2.0...v1.3.0) (2026-09-06)


### Features

* **cli:** full-review groups advisories, security risks and bugs ([5e56ee7](https://github.com/jdziat/open-nitpick/commit/5e56ee78913ad3b6a75564b6ae71f2e5197f1797))
* **cli:** identify-model for TypeScript, from the experiment that cleared it ([a4309a9](https://github.com/jdziat/open-nitpick/commit/a4309a9f221894d824f96071c281489a57eedd88))
* **cli:** repo-score, three numbers per language with their denominators ([daf69c7](https://github.com/jdziat/open-nitpick/commit/daf69c761b7dcf1c8bdc28a5f6638e9c4227c490))
* **evals:** the full-review acceptance fixture and test ([4c5d04b](https://github.com/jdziat/open-nitpick/commit/4c5d04b909960fef12327ac8aecc170998c91e1b))
* full-review, the slop class, repo-score, identify-model ([01c4d99](https://github.com/jdziat/open-nitpick/commit/01c4d99162fd039794bea55bbabb39e971783b27))
* **mcp:** the review engine as MCP tools, with a plugin and skills ([084605b](https://github.com/jdziat/open-nitpick/commit/084605b1043f25d0a17dc7a59600844e41f3c260))
* **mcp:** the review engine as MCP tools, with a plugin and skills ([4258f44](https://github.com/jdziat/open-nitpick/commit/4258f444cbed57d031e96c8b265378a95d532a7a))
* **modelid:** a cross-generation experiment, train on one corpus and test on the next ([64f5e20](https://github.com/jdziat/open-nitpick/commit/64f5e20864a0f408cacd47fc05a30257978f8609))
* **modelid:** a fingerprint instrument clears every language ([703d567](https://github.com/jdziat/open-nitpick/commit/703d567498b9e5229eef589b25b8e396360c7d26))
* **modelid:** fingerprints clear every language; the contributor experiment; docs without tells ([727a8f2](https://github.com/jdziat/open-nitpick/commit/727a8f2c6bde6c0108dd69bef9492b4c4e5313ce))
* **modelid:** the model-identification experiment and its corpus generator ([340b215](https://github.com/jdziat/open-nitpick/commit/340b2150037a355999b38de98677660c259e8162))
* **review:** the slop class, its prompt layer, its expert and its corpus ([84fd7f3](https://github.com/jdziat/open-nitpick/commit/84fd7f3cb6d2a4249e9e3f920cc47b041a1191d3))


### Fixes

* **bundle:** an unbalanced Rust use tree no longer overflows the stack ([37088b6](https://github.com/jdziat/open-nitpick/commit/37088b6494f57ba9922319f6a6ad87d6cb63d1c4))
* **cli:** explain-config prints through checked closures, as both linters ask ([7c795b1](https://github.com/jdziat/open-nitpick/commit/7c795b1308832d47255a5d17e804b38e5540b37b))
* **contrib-corpus:** acknowledge the clone's removal error, as errcheck asks ([e330504](https://github.com/jdziat/open-nitpick/commit/e33050482ceaf409e8995b4b7801c7c4b8aa9dd2))
* **fullreview:** coverage that cannot lie ([a034e5d](https://github.com/jdziat/open-nitpick/commit/a034e5d495f052fac8bd25e226107369d6010a4b))
* **fullreview:** coverage that cannot lie, and the analyzers full review ([d55bb97](https://github.com/jdziat/open-nitpick/commit/d55bb97e31a1d270e73d36f391ab5ea4869b803c))
* **github:** a pull request the API will not diff is diffed from the checkout ([469535e](https://github.com/jdziat/open-nitpick/commit/469535e494a347e4149a0394f3389811f9d43a49))
* **linters:** osv-scanner advisories reach the report ([4d54fdc](https://github.com/jdziat/open-nitpick/commit/4d54fdcb46bbfb504446f5be90d61c9ed12fbd06))
* **linters:** the full review's findings over the analyzers package ([d3d3066](https://github.com/jdziat/open-nitpick/commit/d3d3066126ee8ac9c2986b69f522dc4005d2b4a2))
* **modelid:** the third review's four findings ([08bea56](https://github.com/jdziat/open-nitpick/commit/08bea56a64012e33bf1ae984578f3d6e77b78be2))
* **review:** a code-smell and ai-slop pass over the tree with nitpick's own tools ([d103bbb](https://github.com/jdziat/open-nitpick/commit/d103bbbbe80339cb7004aefa033c63b3388b950a))
* **review:** the branch review's confirmed findings ([8f88176](https://github.com/jdziat/open-nitpick/commit/8f881769eab7aecd52e27d8ebb5bcc62473f5d14))
* **review:** the eighth review's two findings ([3fa1039](https://github.com/jdziat/open-nitpick/commit/3fa1039ce67f32a4075a4f71c3f902bb9f575fff))
* **review:** the fifth review's findings ([bbae602](https://github.com/jdziat/open-nitpick/commit/bbae60283e4844ea67593014649355ff601719b0))
* **review:** the first code-smell and ai-slop pass over the tree ([02c8ef4](https://github.com/jdziat/open-nitpick/commit/02c8ef49a7d4e4dfff9ecabe3190ba1ecde21272))
* **review:** the five findings of the branch's own review ([3e8e82d](https://github.com/jdziat/open-nitpick/commit/3e8e82d0e1d5bb11f77f76aa7dfb6988d382b592))
* **review:** the fourth review's findings ([b071e17](https://github.com/jdziat/open-nitpick/commit/b071e17fc5eee3910f06b5a010311a3ebe161cc8))
* **review:** the second review's three findings ([4b10f92](https://github.com/jdziat/open-nitpick/commit/4b10f92f9ae21c6efd1b35fc9cd0a94b46c38c5e))
* **review:** the sixth review's findings ([7421d80](https://github.com/jdziat/open-nitpick/commit/7421d80acbb95332dbe645c31c5120dc9685671e))
* **vcs:** the unborn-HEAD check through the promoted method, as staticcheck asks ([c9ad068](https://github.com/jdziat/open-nitpick/commit/c9ad06810530fb8897ffb3739ba726ac8c726b4a))


### Measurement

* **modelid:** the contributor experiment on ten repositories, a mixed result ([d95852d](https://github.com/jdziat/open-nitpick/commit/d95852dc999dc5e1331ebf71c92f510cd7b376bb))
* **modelid:** the second corpus and the cross-generation result ([99610c2](https://github.com/jdziat/open-nitpick/commit/99610c2ca60d84f88491293ed29e389b6610a683))
* **slop:** keywords name only what the planted file holds ([cac6167](https://github.com/jdziat/open-nitpick/commit/cac616760abff5d40b008aa2c68487cb9371c685))


### Prompts

* **slop:** rules 5 and 8 as positive tests the model applies before filing ([fd197fc](https://github.com/jdziat/open-nitpick/commit/fd197fcdfeb9612f4de1cf05877d20b4eeb44e7a))


### Documentation

* **findings:** the slop class, first run ([2840995](https://github.com/jdziat/open-nitpick/commit/284099501c535c503345466e6c6a7865495d1097))
* **findings:** the slop class, second run: the controls are not silent ([5e52ada](https://github.com/jdziat/open-nitpick/commit/5e52adad2190e6f9ae4cebadffb3486e542cdecf))
* **plan:** repo-score acceptance, first run ([1ef4a39](https://github.com/jdziat/open-nitpick/commit/1ef4a39160d5276503ed375d0a3499172c6b6798))
* **plan:** sections 2 to 4 status; generator runs models in parallel ([2388b1e](https://github.com/jdziat/open-nitpick/commit/2388b1ede5878f0355436dcead5f8d5824114ff3))
* remove the writing tells from the README and the docs ([6cbbfb4](https://github.com/jdziat/open-nitpick/commit/6cbbfb4ff79239a90cd6f342b4bb51e264228e0f))

## [1.2.0](https://github.com/jdziat/open-nitpick/compare/v1.1.0...v1.2.0) (2026-09-05)


### Features

* **config:** split related_context into definitions (on) and callers (off) ([c9267ba](https://github.com/jdziat/open-nitpick/commit/c9267bac73bcefcbfcc323fd6b8abf69c85da7c6))


### Fixes

* **test:** the shipped default is synthetic ([e77183e](https://github.com/jdziat/open-nitpick/commit/e77183e849421dc2e76ab248cbb7869fb950a361))
