// Package superpowers is a Go port of obra/superpowers' Pi extension (.pi/extensions/superpowers.ts).
package superpowers

import (
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

const bootstrapMarker = "superpowers:using-superpowers bootstrap for pi"

var sourceFile string

var bootstrapSkill string

func skillsDir() string { return "" }

func bootstrapContent() string { return "" }

func stripFrontmatter(content string) string { return content }

func messageContainsBootstrap(message any) bool { return false }

func firstNonCompactionSummaryIndex(messages []any) int { return 0 }

// Extension registers nothing yet.
func Extension() *sdk.Extension { return sdk.New("superpowers") }
