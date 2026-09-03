# open-nitpick's tflint config: the bundled terraform ruleset, recommended
# preset. Plugins that need `tflint --init` are not enabled here.
plugin "terraform" {
  enabled = true
  preset  = "recommended"
}
