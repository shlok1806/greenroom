// Package helper holds the Swift sources of the guest's input helper and agent (daemon ADR
// 0005). The daemon embeds and compiles them in the guest (machine.installHelperScript); this
// Go package exists only for swift_test.go, which typechecks every source on the host and runs
// the host tests in tests/ against logic/.
package helper
