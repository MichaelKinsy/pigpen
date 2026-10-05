// Package ownmodel answers the same typed questions as the TypeSafe API with the
// model PiG is configured with. It is a port of system-one-adapter-python
// (https://github.com/typesafe-ai/system-one-adapter-python, MIT): the same prompts,
// the same per-question answer schemas, probability and discrete answer modes,
// probability normalization, confidence metrics, corrective retries on malformed
// output, and transient retries through the typesafe retry policy.
//
// A [Backend] implements typesafe.Evaluator, so an extension can hold one
// typesafe.Evaluator and pick the TypeSafe API or the own model by configuration.
// No provider or model name is built in: the model is whatever the [Model] the
// caller passes is; package pigmodel provides a Model backed by the PiG Go SDK's
// ModelRegistry (the model PiG is configured with, its auth, its provider layer).
//
// Content is sent to the configured model's provider, not to TypeSafe.
package ownmodel
