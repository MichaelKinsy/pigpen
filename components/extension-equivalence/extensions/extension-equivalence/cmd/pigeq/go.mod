module github.com/MichaelKinsy/pigpen/extension-equivalence/cmd/pigeq

go 1.26

// A module of its own: PiG rejects an extension directory that holds both a factory and a
// standalone main package, so the command line sits beside the factory, not in its module.
require github.com/MichaelKinsy/pigpen/extension-equivalence v0.0.0

replace github.com/MichaelKinsy/pigpen/extension-equivalence => ../..
