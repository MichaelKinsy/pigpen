//go:build pighost

// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Michael Kinsy

package intent_test

// Adds PiG's real host validator to TestZZOmpDifferential. The extension module does not require the PiG
// host module, so this file only builds with a modfile that adds it (run-port.sh --host does that):
//
//	require github.com/MichaelKinsy/PiG v0.0.0
//	replace github.com/MichaelKinsy/PiG => <PIG_SRC>

import "github.com/MichaelKinsy/PiG/ai"

func init() { ompDiffHostValidate = ai.ValidateToolArgumentsJSON }
