// Package typesafe is a Go client for the TypeSafe AI API: a port of the official
// TypeScript SDK @typesafe-ai/sdk 0.6.0 (https://github.com/typesafe-ai/typesafe-sdk-js,
// MIT; see the component's CREDITS.md and port/PORT.md for the pinned commit and the
// file-by-file mapping).
//
// A request carries a state (text, a JSON object or array, or null) and named typed
// questions. Each answer is typed by the question that produced it:
//
//	client, err := typesafe.NewClient(typesafe.Config{}) // TYPESAFE_API_KEY from the environment
//	res, err := client.SystemOne(ctx, typesafe.SystemOneRequest{
//		State: typesafe.Text("I was charged twice. Please fix this ASAP."),
//		Questions: typesafe.Questions{
//			typesafe.Ask("category", typesafe.Choice("What is this ticket about?",
//				typesafe.Opt("billing", nil), typesafe.Opt("technical", nil), typesafe.Opt("other", nil))),
//		},
//	}, nil)
//	answer, err := res.Choice("category") // ChoiceAnswer{Choice, Confidence, Probabilities}
//
// The three question kinds are Noul (yes/no), Choice (named alternatives) and Score
// (an ordered rubric). The [Evaluator] interface is what code that only needs answers
// depends on; [Client] (the TypeSafe API) and the own-model backend in package
// ownmodel both implement it. [EvaluateBatch] runs many requests through an Evaluator
// with bounded concurrency.
//
// The package uses the standard library only. See CONTRACT.md in the component root
// for the deliberate differences from the TypeScript SDK.
package typesafe
