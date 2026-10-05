package mapper_test

import "github.com/MichaelKinsy/pigpen/ahp/internal/testkit"

func checkAction(a any) error { return testkit.CheckSchema("actions", "StateAction", a) }
