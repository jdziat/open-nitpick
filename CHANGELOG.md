# Changelog

## [1.5.1](https://github.com/jdziat/open-nitpick/compare/v1.5.0...v1.5.1) (2026-09-07)


### Fixes

* **docs:** the prose tells the word rules could not see ([#17](https://github.com/jdziat/open-nitpick/issues/17)) ([9e0aac0](https://github.com/jdziat/open-nitpick/commit/9e0aac0196dc84ba491e377d2700878656c89762))


### Documentation

* the status section and the aphorisms the cadence rule misses ([#19](https://github.com/jdziat/open-nitpick/issues/19)) ([8ad4eea](https://github.com/jdziat/open-nitpick/commit/8ad4eea391e6fe453fa13e98b27ea5595c6b2a67))

## [1.5.0](https://github.com/jdziat/open-nitpick/compare/v1.4.0...v1.5.0) (2026-09-07)


### Features

* **budget:** a spending ceiling that reviews the top files under it ([#15](https://github.com/jdziat/open-nitpick/issues/15)) ([7d8d318](https://github.com/jdziat/open-nitpick/commit/7d8d318831a996b2228d02738bf2269deee79265))
* **spend:** only the repository people can make the reviewer spend ([#16](https://github.com/jdziat/open-nitpick/issues/16)) ([aefb97c](https://github.com/jdziat/open-nitpick/commit/aefb97c2b9a48070e6cc875f07fad76530d7cae6))


### Fixes

* **launch:** pin the composite action and close the doc gaps ([#10](https://github.com/jdziat/open-nitpick/issues/10)) ([74aaf71](https://github.com/jdziat/open-nitpick/commit/74aaf71e9772170399f6cc9f71ac2177659cb55c))

## [1.4.0](https://github.com/jdziat/open-nitpick/compare/v1.3.0...v1.4.0) (2026-09-06)


### ⚠ BREAKING CHANGES

* **repo:** remove the contributor corpus, ten projects' source code

### Fixes

* **repo:** remove the contributor corpus, ten projects' source code ([a8a18bd](https://github.com/jdziat/open-nitpick/commit/a8a18bd2f3994c26e6a83ae46f93fd64c49e0190))


### Documentation

* **site:** the magnifier mark replaces the four-bar logo ([cb6db6f](https://github.com/jdziat/open-nitpick/commit/cb6db6f0705d296412877a3c8aea180bf6b99749))
* the corpus tooling is gone, so the docs stop pointing at it ([#8](https://github.com/jdziat/open-nitpick/issues/8)) ([22b2a14](https://github.com/jdziat/open-nitpick/commit/22b2a144d29e164b194bf43566cd19dc876ce84a))
* the launch blockers two reviews found ([1736843](https://github.com/jdziat/open-nitpick/commit/1736843a086b2c3610175c80d73206cf192214f2))

## 1.3.0 (2026-09-06)


### ⚠ BREAKING CHANGES

* **module:** the module path carries the v2 suffix
* **cli:** remove identify-model, whose answer does not carry to real code

### Features

* **bundle:** file batching under a token budget ([7217c7e](https://github.com/jdziat/open-nitpick/commit/7217c7e7d74dbd06a9faabc48f010b118087d835))
* **bundle:** fit context to the budget, and stop a path forging the prompt ([d6ca00c](https://github.com/jdziat/open-nitpick/commit/d6ca00caca97371d00a9c918f54865089af98be8))
* **cli:** full-review groups advisories, security risks and bugs ([ee77dc5](https://github.com/jdziat/open-nitpick/commit/ee77dc5a61c32997899380a22a4f693972f4b592))
* **cli:** full-review reviews the whole tree with a remediation plan ([ac498c9](https://github.com/jdziat/open-nitpick/commit/ac498c9ac584e96aa7057eec42b219948c7f5e47))
* **cli:** identify-model for TypeScript, from the experiment that cleared it ([c64cec4](https://github.com/jdziat/open-nitpick/commit/c64cec440c44de133ee1d0a99ac5a129b33574fd))
* **cli:** nitpick slop scores AI slop and says how to fix it ([663eac2](https://github.com/jdziat/open-nitpick/commit/663eac2bec19d96f0722758d3170bfc9e9442055))
* **cli:** nitpick slop scores AI slop and says how to fix it ([c52e0ae](https://github.com/jdziat/open-nitpick/commit/c52e0aefd71581a1b5ad826f32c442f0114d2a1f))
* **cli:** progress logs during a review, and mcp install for agent clients ([7032bb5](https://github.com/jdziat/open-nitpick/commit/7032bb50c75c14b572247827ea9ab33c6e9e8b8b))
* **cli:** progress logs during a review, and mcp install for eight agent clients ([2f65b7c](https://github.com/jdziat/open-nitpick/commit/2f65b7cab31f07f530a1974b1f5d0dfde921331f))
* **cli:** remove identify-model, whose answer does not carry to real code ([3bc288e](https://github.com/jdziat/open-nitpick/commit/3bc288e5c47333ab1a4d47000d86023d391349be))
* **cli:** repo-score, three numbers per language with their denominators ([586cee5](https://github.com/jdziat/open-nitpick/commit/586cee555acc5faaba4b441734d0e0aee090fb2b))
* **cmd:** nitpick CLI ([cea2399](https://github.com/jdziat/open-nitpick/commit/cea23995c31c611f08da4450e4f215585682630c))
* **config:** .nitpick.yaml policy, personas, and trust boundary ([df48ea0](https://github.com/jdziat/open-nitpick/commit/df48ea06eb9ec5ca8ab93140b285d9dc4191c85c))
* **config:** split related_context into definitions (on) and callers (off) ([0063a6a](https://github.com/jdziat/open-nitpick/commit/0063a6a6efa05ab4194bdc41d04b3365e60e27de))
* **diff:** unified diff parsing and GitHub position mapping ([6af6d90](https://github.com/jdziat/open-nitpick/commit/6af6d9071b77e34bfcdcbf7ba00b1d2fc0f62d77))
* **evals:** balance the corpus and exercise batching for the first time ([e0b5d8f](https://github.com/jdziat/open-nitpick/commit/e0b5d8f9902f39c4cb02e13f5d7e752a0824c009))
* **evals:** judge-swap on cached findings, and severity ground truth ([39a16cf](https://github.com/jdziat/open-nitpick/commit/39a16cfa22452cf44e58c540b7651a930ae7858d))
* **evals:** let a run set the per-review timeout ([ce2762b](https://github.com/jdziat/open-nitpick/commit/ce2762bd7a36a38e9b92ddcc4c3a2daa66037eb2))
* **evals:** model evaluation harness behind the eval build tag ([d9531f0](https://github.com/jdziat/open-nitpick/commit/d9531f0868ba33ecd224d05d23b62b99b89d797e))
* **evals:** report grade spread, and run our side more than once ([bee8647](https://github.com/jdziat/open-nitpick/commit/bee864768e9ed881e371b6e53015ca440dd5fd26))
* **evals:** retain the raw Incumbent review in the cache ([3c7f509](https://github.com/jdziat/open-nitpick/commit/3c7f5092cf0490f42b0a28cff3f31db233e5ec6e))
* **evals:** score severity against ground truth, add held-out corpus ([8c127ee](https://github.com/jdziat/open-nitpick/commit/8c127ee396979212dd64c018ad16876118ebad99))
* **evals:** stop scoring severity across vocabularies, and guard the description ([594bc32](https://github.com/jdziat/open-nitpick/commit/594bc327bf52385d7b65a674f4aad3c096c0c4d2))
* **evals:** the full-review acceptance fixture and test ([fd632d8](https://github.com/jdziat/open-nitpick/commit/fd632d8227c2de0f83c2a3c5ac70f9e0a7000373))
* full-review, the slop class, repo-score, identify-model ([8679dbf](https://github.com/jdziat/open-nitpick/commit/8679dbff65cdeb336e652cb5972af8b07c123b3a))
* incremental review on push, and related context from files the change does not touch ([57500cd](https://github.com/jdziat/open-nitpick/commit/57500cd2849ea999162f497d3d2177f99bc776e6))
* **linters:** analyzer runners as evidence for triage ([1dfc5e9](https://github.com/jdziat/open-nitpick/commit/1dfc5e9cde859babe294fccb72613eb876425e84))
* **llm:** make openrouter a first-class provider and the default ([d745889](https://github.com/jdziat/open-nitpick/commit/d7458895489acdf2c594ac338b2c222fda2a2c87))
* **llm:** provider registry and structured output extraction ([44bed33](https://github.com/jdziat/open-nitpick/commit/44bed331995b5cb974bd30d60bc326cc005667e8))
* **mcp:** the review engine as MCP tools, with a plugin and skills ([bfce7b8](https://github.com/jdziat/open-nitpick/commit/bfce7b80f4871d886fa84c1973460f104c0594dd))
* **mcp:** the review engine as MCP tools, with a plugin and skills ([b8cfc56](https://github.com/jdziat/open-nitpick/commit/b8cfc5643154c3b55836e09a667289aee142e5f3))
* **modelid:** a cross-generation experiment, train on one corpus and test on the next ([e2ec2a3](https://github.com/jdziat/open-nitpick/commit/e2ec2a3af57ce2753640de6d8016bf860385b78e))
* **modelid:** a fingerprint instrument clears every language ([5889a6c](https://github.com/jdziat/open-nitpick/commit/5889a6cc4ba2706fca4513d3278fd5de1119aa03))
* **modelid:** fingerprints clear every language; the contributor experiment; docs without tells ([236d033](https://github.com/jdziat/open-nitpick/commit/236d033c23044a400cd4320b8ecd307086351a4a))
* **modelid:** the model-identification experiment and its corpus generator ([5e58668](https://github.com/jdziat/open-nitpick/commit/5e5866878988b48a1658245e1bc70a99ac25e3d8))
* **prompt:** layered templates with persona and nitpick scope ([170393f](https://github.com/jdziat/open-nitpick/commit/170393fca34578b09a0f6522f9d40dc19f9c4cea))
* **review:** [skip review] in a title, body or head commit skips the run ([333e28f](https://github.com/jdziat/open-nitpick/commit/333e28fd855dc053151eeb847ccafe30fd866f01))
* **review:** [skip review] in a title, body or head commit skips the run ([9fafb63](https://github.com/jdziat/open-nitpick/commit/9fafb63543ed13daeac758ac73d1676229ef788e))
* **review:** an incremental run resolves the comments it has outgrown ([0877295](https://github.com/jdziat/open-nitpick/commit/0877295a130bcfd3ba6aa7cb106bc9647de2ad12))
* **review:** an incremental run resolves the comments it has outgrown ([f8d5b74](https://github.com/jdziat/open-nitpick/commit/f8d5b74ceff8fec9868bc246cdbc116a197f7c8b))
* **review:** engine, hand-authored schema, and rendering ([14627bb](https://github.com/jdziat/open-nitpick/commit/14627bb422d894a8d3c73b5665adec1c2f30ada2))
* **review:** route each finding to a domain expert before publishing it ([b7d80c4](https://github.com/jdziat/open-nitpick/commit/b7d80c4594fb59596a736da568966303cc370dab))
* **review:** the [@nitpick](https://github.com/nitpick) conversation on a pull request ([22347dd](https://github.com/jdziat/open-nitpick/commit/22347dd4f2743cdda0a2c5f9c8b8f2627c6a6980))
* **review:** the [@nitpick](https://github.com/nitpick) conversation on a pull request ([9eaf42c](https://github.com/jdziat/open-nitpick/commit/9eaf42ca954f6e421287ca5b63fd33c5f7b27c87))
* **review:** the slop class, its prompt layer, its expert and its corpus ([5c47d26](https://github.com/jdziat/open-nitpick/commit/5c47d260b2c4848a5ae50fb33623fedd3dfb2751))
* **vcs:** github and local providers ([e7f4149](https://github.com/jdziat/open-nitpick/commit/e7f41495e91f7b82e506f28af7bc524cc300230f))


### Fixes

* **bundle:** an unbalanced Rust use tree no longer overflows the stack ([2f381b8](https://github.com/jdziat/open-nitpick/commit/2f381b8ae78e0024a715cce19cbfdf0ace2b9e1a))
* **bundle:** stop reporting diff-only reviews as unreviewed files ([7a70a27](https://github.com/jdziat/open-nitpick/commit/7a70a27f5182c583694226b0b3abb1c0b881ce35))
* **cli:** explain-config prints through checked closures, as both linters ask ([8a8e0b0](https://github.com/jdziat/open-nitpick/commit/8a8e0b0f94e2e22ad9d230bceb7567ffd033e29c))
* **cli:** the slop pass over cmd, its tells and its hidden findings ([d361e85](https://github.com/jdziat/open-nitpick/commit/d361e85d4b308fddcc211a93d7d91295a8cc13a6))
* **cli:** the slop pass over cmd, its tells and its hidden findings ([9294d03](https://github.com/jdziat/open-nitpick/commit/9294d034103d8912483adb12ab17f968f87b46db))
* **config:** a change may not supply the policy it is reviewed under ([90dbc9e](https://github.com/jdziat/open-nitpick/commit/90dbc9e2d746f576b922c5eb1ed357b8652aca75))
* **config:** pin kimi-k3 on OpenRouter to Moonshot's endpoint, with fallbacks ([fa97821](https://github.com/jdziat/open-nitpick/commit/fa9782101bde57161ca13413795ca04a20561fdf))
* **config:** pin kimi-k3 on OpenRouter to Moonshot's endpoint, with fallbacks ([759af03](https://github.com/jdziat/open-nitpick/commit/759af033779acd87f12eae63d914520bb796addf))
* **contrib-corpus:** acknowledge the clone's removal error, as errcheck asks ([d8040f4](https://github.com/jdziat/open-nitpick/commit/d8040f4397b17836e56a1f60c19c1d8a2371a33c))
* **evals:** close four cross-fixture keyword leaks, and revisit the major mapping ([0be72f0](https://github.com/jdziat/open-nitpick/commit/0be72f0c6ab1781f01c016d6267d9e667760862e))
* **evals:** compare rates, and key comparability on fixture coverage ([7a2a75f](https://github.com/jdziat/open-nitpick/commit/7a2a75fb1bbd18eaa8f2778a64ea9e1e8cb2a4c7))
* **evals:** make benchmark honour RUNS, and say what SPREAD measures ([ce41ef3](https://github.com/jdziat/open-nitpick/commit/ce41ef3850fb9ffdc4fe8f4f882bf4893a63b495))
* **evals:** read Incumbent's secondary locations, and re-derive from raw ([21adad7](https://github.com/jdziat/open-nitpick/commit/21adad7883cb3fa7bcbada6eb7586bb3ef5684db))
* **evals:** rebuild the Incumbent benchmark on plain-text mode ([3712dcc](https://github.com/jdziat/open-nitpick/commit/3712dcc04e35373809fb8fc8092c3ecbf936d1a5))
* **evals:** repair keyword recall, and put the adversary inside the round ([a4fdf59](https://github.com/jdziat/open-nitpick/commit/a4fdf59f65c14143d6e69a09357c01cac5ce903b))
* **evals:** report understated severities alongside inflated ones ([800b642](https://github.com/jdziat/open-nitpick/commit/800b642672729a44b817c330c71405c18b4cb5b0))
* **evals:** run the degeneracy guards through the real scorer ([d38c0cb](https://github.com/jdziat/open-nitpick/commit/d38c0cbf14a8dc8200b9d3a42d31b7c00d3b6623))
* **evals:** score a multi-line anchor from its nearest edge ([33e8858](https://github.com/jdziat/open-nitpick/commit/33e8858de0f45bf55d6b94db78717988973f2e10))
* **evals:** withdraw the cross-tool severity score, and guard degeneracy ([dd4c9d2](https://github.com/jdziat/open-nitpick/commit/dd4c9d2c0f953bb118de7ea97e4eb41fb49ed765))
* **fullreview:** coverage that cannot lie ([3b3493f](https://github.com/jdziat/open-nitpick/commit/3b3493fb3c3afa17950d02b79f54e8e638c2bda4))
* **fullreview:** coverage that cannot lie, and the analyzers full review ([0506f4f](https://github.com/jdziat/open-nitpick/commit/0506f4f213f980c37b35765a919eec4dec22a807))
* **github:** a pull request the API will not diff is diffed from the checkout ([8744279](https://github.com/jdziat/open-nitpick/commit/8744279e02db58715a524bfb460c382b5175fe59))
* **linters:** a change may not supply the analyzer configuration it is reviewed under ([71d413b](https://github.com/jdziat/open-nitpick/commit/71d413be12211841ad888870e37eed7dc257deb1))
* **linters:** close the source-level channels that silenced or moved a finding ([2a93c3e](https://github.com/jdziat/open-nitpick/commit/2a93c3eac16638fe86a0e6131825303cf0343dd4))
* **linters:** let an analyzer's CRITICAL reach the gate, under an operator ceiling ([c343982](https://github.com/jdziat/open-nitpick/commit/c3439823a035899998700e4bdafa4b31657d9fcb))
* **linters:** osv-scanner advisories reach the report ([f6ee183](https://github.com/jdziat/open-nitpick/commit/f6ee1830cbf72e9d797da6ed95a41c21e131f727))
* **linters:** the full review's findings over the analyzers package ([98e686d](https://github.com/jdziat/open-nitpick/commit/98e686d05e29899699a9aefa79e564648325e968))
* **make:** cover DUMP in the pass-through fix, by pattern not by list ([e95fc71](https://github.com/jdziat/open-nitpick/commit/e95fc7170c4244f26202658d89fea7263c981778))
* **make:** stop empty variables clobbering an exported eval setting ([0ce18b3](https://github.com/jdziat/open-nitpick/commit/0ce18b31e4a50b2527fff13e208a4acc8f263629))
* **modelid:** the third review's four findings ([84fae30](https://github.com/jdziat/open-nitpick/commit/84fae30d3879f3013699de88b0b587e8e04ea873))
* **module:** the module path carries the v2 suffix ([3c35fc4](https://github.com/jdziat/open-nitpick/commit/3c35fc4a0f92bdfdf1ad6a486b74993c1a2af061))
* **respond:** a comment event names its pull request itself ([243c185](https://github.com/jdziat/open-nitpick/commit/243c185bf0bd40208b57a874389af93e8858e620))
* **respond:** a comment event names its pull request itself ([728d71b](https://github.com/jdziat/open-nitpick/commit/728d71b55818485006b7d1ad352c45717f1994af))
* **review:** a code-smell and ai-slop pass over the tree with nitpick's own tools ([8d70682](https://github.com/jdziat/open-nitpick/commit/8d70682109ccbf88b2721b2b77617e996d60ed34))
* **review:** the branch review's confirmed findings ([b5a24b9](https://github.com/jdziat/open-nitpick/commit/b5a24b9dc7a4ed500fde6c461c31db0e838c2580))
* **review:** the eighth review's two findings ([bf9980d](https://github.com/jdziat/open-nitpick/commit/bf9980dfa14657d75e073c22846464451029622e))
* **review:** the fifth review's findings ([1802cec](https://github.com/jdziat/open-nitpick/commit/1802cece5866dd7d462a6a75a4016b052da58fc0))
* **review:** the first code-smell and ai-slop pass over the tree ([5b79413](https://github.com/jdziat/open-nitpick/commit/5b794135aa1f6da5d45fd63e4d8a63840ab7e127))
* **review:** the five findings of the branch's own review ([c83b652](https://github.com/jdziat/open-nitpick/commit/c83b652e694b57c8f07c7d7fbc0b2dca10440b28))
* **review:** the fourth review's findings ([d8c37f0](https://github.com/jdziat/open-nitpick/commit/d8c37f044681b68fbfa1360e280d507949c36558))
* **review:** the second review's three findings ([7a3bef3](https://github.com/jdziat/open-nitpick/commit/7a3bef3e55263053b8c34c1e538834b87dba0590))
* **review:** the sixth review's findings ([d7ea0eb](https://github.com/jdziat/open-nitpick/commit/d7ea0eb36f31e8316deb39fa308dfb88d12b8799))
* **test:** the shipped default is synthetic ([6102968](https://github.com/jdziat/open-nitpick/commit/610296800a58b8d36c391d0f721bb629bf91fef9))
* **vcs:** the unborn-HEAD check through the promoted method, as staticcheck asks ([473c2fb](https://github.com/jdziat/open-nitpick/commit/473c2fbd2cab027bf1891016c8e53d48a3ade5ce))
* **voice:** the four findings its own review made ([40e8127](https://github.com/jdziat/open-nitpick/commit/40e81271a4644838046796ec84ae9760efaa349f))


### Measurement

* a capability comparison, a per-language report from retained runs, and iteration 1 on noise ([a870685](https://github.com/jdziat/open-nitpick/commit/a870685b41514f11fa884e11767368f1b8c22e53))
* a multi-file corpus, a Contender adapter, and the first spend of both ([9683a39](https://github.com/jdziat/open-nitpick/commit/9683a3997756ff8aaf42f251712715b7c38d08db))
* an info corpus — ten info plants and two clean controls, the band no reviewer had located (workstream 6) ([a3b4bd4](https://github.com/jdziat/open-nitpick/commit/a3b4bd47acc6fded12ff62e7c20c933a386742d2))
* callers corpus ([da7bebd](https://github.com/jdziat/open-nitpick/commit/da7bebd0baf90aa4c5438aa6724a1a4e3fd181dd))
* denoise variants of the routed and ensemble configs; opus-5 priced ([19665eb](https://github.com/jdziat/open-nitpick/commit/19665eb6853bede1815560d8603bd9c13a642e2d))
* denoise variants written properly ([447a908](https://github.com/jdziat/open-nitpick/commit/447a9089a58a10a9a2eb855bec9e01a0fba389ed))
* glm-5.3-flash as the iteration model, priced, measured, and made the triage default ([f6eaed9](https://github.com/jdziat/open-nitpick/commit/f6eaed9df320968197e0e8b682b8f083e45b5e70))
* incumbent caches for the callers corpus ([13eafd5](https://github.com/jdziat/open-nitpick/commit/13eafd5d2ab5c78bce32ed11bba6f50233807823))
* make the head-to-head answer its own ship rule, and amend the rule ([8bb54c9](https://github.com/jdziat/open-nitpick/commit/8bb54c914879e7b97da5f6f2fda259d5cadd8b1d))
* **modelid:** the contributor experiment on ten repositories, a mixed result ([f213e91](https://github.com/jdziat/open-nitpick/commit/f213e9139c48a71a84b5a3b4b1f50544f12e30c8))
* **modelid:** the second corpus and the cross-generation result ([7ac0771](https://github.com/jdziat/open-nitpick/commit/7ac077181a4635f3448a5bf7cad8c94d84c475e3))
* NITPICK_EVAL_ENGINE_LOG keeps the engine's warnings from a battery ([77524dd](https://github.com/jdziat/open-nitpick/commit/77524ddd6b47efc1d6cd34ae1eca58062717173d))
* price five more models into the battery ([c5bd43b](https://github.com/jdziat/open-nitpick/commit/c5bd43b68575a98a37bab5961672d90a6c0d2235))
* price qwen3.8-flash and glm-5.3 into the battery ([7f5ac62](https://github.com/jdziat/open-nitpick/commit/7f5ac62a4daf785e56c972d1a928014210ea250b))
* prices for two Gemma 4 variants ([639d5bc](https://github.com/jdziat/open-nitpick/commit/639d5bc6e281aeca9f697c1d95cca672d0b52494))
* re-collected Incumbent reviews for the two corrected fixtures ([ed32a92](https://github.com/jdziat/open-nitpick/commit/ed32a9271986dce203adcecb13bc63fa11fabd60))
* remove two unplanted defects the corpus shipped, credit two phrasings the scorer missed ([f15586d](https://github.com/jdziat/open-nitpick/commit/f15586df34b6438be4804503f4147e6907d10351))
* **slop:** keywords name only what the planted file holds ([46af023](https://github.com/jdziat/open-nitpick/commit/46af0233e357da4b4505ee1be6284e7d4e9fee98))
* strong model moves from triage to validate; glm + kimi expert route ([c239c39](https://github.com/jdziat/open-nitpick/commit/c239c39fbedcc06aa784af17c3cd6637e87cd1d8))
* Synthetic usage-based rates for its seven always-on models, operator-captured ([e294198](https://github.com/jdziat/open-nitpick/commit/e294198edb315857285cae38b239062aedc69853))


### Prompts

* keep the Qwen note, drop the GLM and DeepSeek ones after ablation; docs: the tuning pass and Synthetic results ([54cd1a4](https://github.com/jdziat/open-nitpick/commit/54cd1a42869597094c1f34885df9a2c204a46f72))
* model-family guidance layer, two base rules on reachability and untouched helpers; llm: synthetic provider; evals: provider-prefixed model ids ([4399fff](https://github.com/jdziat/open-nitpick/commit/4399fff74d59d5a15efc686664ea46bd05c1d639))
* **respond:** the answer is written without em dashes ([60d217f](https://github.com/jdziat/open-nitpick/commit/60d217fe5356e975320623e70c4b1b1fb41c5404))
* **slop:** rules 5 and 8 as positive tests the model applies before filing ([c24c46f](https://github.com/jdziat/open-nitpick/commit/c24c46f82d150d817bf0e24363eca07c1d900188))
* stop the severity ladder naming what the corpus plants, and guard it ([aec7876](https://github.com/jdziat/open-nitpick/commit/aec78763708b280736be2821fa3e226ee876591a))
* the maintainability example was a correctness bug — and fixing it changed nothing ([3fae93a](https://github.com/jdziat/open-nitpick/commit/3fae93a3904975ae15c8a30bf0d1f08f9ce83d10))
* **voice:** every role writes short, and the tells are scrubbed ([e016188](https://github.com/jdziat/open-nitpick/commit/e01618889fdab0ce47877894d37deba0d03c6916))


### Documentation

* **config:** the OpenRouter config link survives the site build ([54b4d73](https://github.com/jdziat/open-nitpick/commit/54b4d73092ff1eeed23636dbeeb3c88716ad4c31))
* **config:** the repository's policy on OpenRouter ([2e3f4a6](https://github.com/jdziat/open-nitpick/commit/2e3f4a6bedd26d5e0e04e511e799044e0363c471))
* **config:** the repository's policy on OpenRouter, as .nitpick.openrouter.yaml ([e4f6035](https://github.com/jdziat/open-nitpick/commit/e4f6035ff13d455fc116e1c46c8da7578e962069))
* cost/performance sweep across twelve models on three corpora ([c7a2086](https://github.com/jdziat/open-nitpick/commit/c7a2086ea538ead935887129d6341a68cc18e188))
* **evals:** re-collect the incumbent on the full corpus, and correct the baseline ([f53e082](https://github.com/jdziat/open-nitpick/commit/f53e08224635b07f3da8897c6b35f383e058cafe))
* **evals:** record the v1 head-to-head ([d501b03](https://github.com/jdziat/open-nitpick/commit/d501b033f9cfda2d42b027ed7c470f3bf23faf8d))
* **findings:** the slop class, first run ([6403137](https://github.com/jdziat/open-nitpick/commit/64031378b9feefa21a0607b51cfbe543a8ebd7a2))
* **findings:** the slop class, second run: the controls are not silent ([e353a6d](https://github.com/jdziat/open-nitpick/commit/e353a6d76b17252944f0228777edb0d0758eaecd))
* gemma-4-31b pinned to deepinfra/turbo; providers in the README ([3c371bd](https://github.com/jdziat/open-nitpick/commit/3c371bdfb6dc71a1ed2fc008bc5da0bb481c65e6))
* gpt-5.6-luna re-measured on the current prompt ([3541af3](https://github.com/jdziat/open-nitpick/commit/3541af316fc34e344f97ef70f2b2ac7528acd481))
* Incumbent's first full run is the comparison figure ([9fd187b](https://github.com/jdziat/open-nitpick/commit/9fd187b95bcf60406134b4d4ee778ed4f6929dc7))
* kimi-k3 priced and measured beside sonnet and glm; the second hop of related context on the fourteen-fixture corpus ([e57cf4a](https://github.com/jdziat/open-nitpick/commit/e57cf4ade156feec17eee9b1f583c2a601761192))
* measured Synthetic cost per review at its usage-based rates ([099a134](https://github.com/jdziat/open-nitpick/commit/099a134b3ebdc1359fc9e47cb6929c620f4e71a8))
* **plan:** full-review, the slop class, repo-score, model identification ([cc933e7](https://github.com/jdziat/open-nitpick/commit/cc933e712de86534ccdb3b913e39c5f57dd246a1))
* **plan:** record the five decisions for full-review ([22f7fca](https://github.com/jdziat/open-nitpick/commit/22f7fca6d262135823bc13adb7913253fb20b327))
* **plan:** repo-score acceptance, first run ([a97f887](https://github.com/jdziat/open-nitpick/commit/a97f887810a8d9056ffd8d5df77dd16c279ba10d))
* **plan:** section 1 status; lint nits ([cb16f79](https://github.com/jdziat/open-nitpick/commit/cb16f79ef6db8fae5d16ce589da89072e588b379))
* **plan:** sections 2 to 4 status; generator runs models in parallel ([6d16284](https://github.com/jdziat/open-nitpick/commit/6d16284508e4bd8074d42b85f540aab93e86998b))
* pre-register the v1 ship decision before reading the battery ([1e55e6e](https://github.com/jdziat/open-nitpick/commit/1e55e6e91b44dc1efbb9981860ff0555fd0049d7))
* price per review beside Incumbent's on-demand rate ([4a9fa58](https://github.com/jdziat/open-nitpick/commit/4a9fa5800657fc66335a0cea2dd027d51c01a186))
* **readme:** the mark above the badges ([3161c60](https://github.com/jdziat/open-nitpick/commit/3161c6012e1adc4df375ca5143abeaadb58fb1b6))
* **readme:** the mark above the badges ([934bb43](https://github.com/jdziat/open-nitpick/commit/934bb431f2549ea32c7684d33c04154d506f76fc))
* **readme:** the mark resolves from the site's guide page ([af7591b](https://github.com/jdziat/open-nitpick/commit/af7591b6d73dee1314e127e0bcffd6f3b28357ed))
* **readme:** the status names what is still not done ([bc5cb70](https://github.com/jdziat/open-nitpick/commit/bc5cb709b256127ba299a15b62b742e3e5a7dafe))
* record what the harness can support, and what it got wrong ([e481e7c](https://github.com/jdziat/open-nitpick/commit/e481e7c047e945d8276e5cd8004424f5b9643947))
* remediation plan for the benchmark misses, each traced to its cause on the pull request ([23983a7](https://github.com/jdziat/open-nitpick/commit/23983a78bf4c8ee108c363da6b160859b37684fe))
* remediation status after every workstream, with what each measured and what it cost ([0d2b485](https://github.com/jdziat/open-nitpick/commit/0d2b485a4d861c98900a79d976c72d7f618a09dc))
* remove the writing tells from the README and the docs ([d97288d](https://github.com/jdziat/open-nitpick/commit/d97288daf5901e3240e8c63a593de666c0e6ba97))
* routing and ensemble results ([ddef062](https://github.com/jdziat/open-nitpick/commit/ddef062634b49f4febe7541a205c315edbf61d7f))
* site, and the README recommends Synthetic ([6cf999c](https://github.com/jdziat/open-nitpick/commit/6cf999c0055f31c1d697db224f1b20b4beec57f3))
* **site:** the four-bar mark as the site's logo, favicon and social card ([ba2c2d0](https://github.com/jdziat/open-nitpick/commit/ba2c2d022d4ab288ef9860a037463ba3c8af4393))
* **site:** the four-bar mark as the site's logo, favicon and social card ([201f83b](https://github.com/jdziat/open-nitpick/commit/201f83ba4e52b86cf761a95a63c64045c21e62ef))
* the benchmark re-run under the remediated code — four of eight misses recovered, and what the extra noise is ([6b2d3da](https://github.com/jdziat/open-nitpick/commit/6b2d3da3f60ddbc19f711cf83275ff307953774e))
* the callers measurement, and the review-round corrections ([6b160c9](https://github.com/jdziat/open-nitpick/commit/6b160c9aa38bb2afd5caf56e45d1c4430f6a8624))
* the expert pass measured (one plant for one noise finding, half again the cost) and left off; where the comparison stops ([e42c450](https://github.com/jdziat/open-nitpick/commit/e42c45061e906b79388dec3b485ef6646a890116))
* the README as a home page, the guide as docs pages ([d9f9dab](https://github.com/jdziat/open-nitpick/commit/d9f9dabc74569e8199b8558a20aa0e425aaec5d9))
* the README as a home page, the guide as docs pages ([15452ba](https://github.com/jdziat/open-nitpick/commit/15452ba8d1c0e5a6c61b35a23c377fdab1bac79c))
* the v1 gate fails condition 3 of its own pre-registered rule ([ae4fb01](https://github.com/jdziat/open-nitpick/commit/ae4fb01172bacabe4a394b1ab8eade1f65f64ac8))
* two Gemma 4 variants, and what the stall retry did for them ([0090a4d](https://github.com/jdziat/open-nitpick/commit/0090a4d05e4587e84ca33fa6a149cf6411d91635))
* v1 passes all four conditions of Rule 14, on the second held-out spend ([c9e46bb](https://github.com/jdziat/open-nitpick/commit/c9e46bbabcede44aae5067fe243f148d63313c82))

## Changelog
