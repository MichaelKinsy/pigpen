module github.com/MichaelKinsy/pigpen/pig-doctor/cmd/pig-doctor

go 1.26

// A module of its own: PiG rejects an extension directory that holds both a factory and a
// standalone main package, so the command line sits beside the factory, not in its module.
require github.com/MichaelKinsy/pigpen/pig-doctor v0.0.0

replace github.com/MichaelKinsy/pigpen/pig-doctor => ../..
