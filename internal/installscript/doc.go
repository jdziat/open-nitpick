// Package installscript holds the tests for website/install.sh and
// website/install.ps1, the scripts that install a nitpick release.
//
// The scripts are shell and PowerShell, so there is no Go to import: the tests
// run them against a fake release server, with a PATH holding only the tools a
// case allows, and read the Makefile and release workflow so that a change to
// how releases are named fails here and not for a user.
package installscript
