# Red run (before the implementation)

`go test ./...` on the stubs (compilable signatures that do nothing), PiG 0.3.0 SDK, go1.27.1:
35 test functions fail, 2 pass by design of the stubs (no-op cases), 1 is a named skipped twin
(`TestParseMouseDecodesSGRReports`). Five of the six packages also abort with a panic on the first
stub misuse (nil source, empty herd), which hides the tests after it; the count above is what ran.

```
--- FAIL: TestExtensionRegistersSnakeCommandsOnly (0.01s)
--- FAIL: TestSpaceOrEnterStartsAndAnyDirectionStartsAndSteers (0.00s)
panic: runtime error: index out of range [0] with length 0 [recovered, repanicked]
FAIL	github.com/MichaelKinsy/pigpen/pig-snake	0.020s
--- FAIL: TestEncodePairsRowsIntoExactWidthHalfBlocks (0.00s)
--- FAIL: TestEncodeFallsBackTo256Colors (0.00s)
panic: runtime error: index out of range [0] with length 0 [recovered, repanicked]
FAIL	github.com/MichaelKinsy/pigpen/pig-snake/internal/pixel	0.008s
--- FAIL: TestGridForPicksTheLargestComfortableHead (0.00s)
--- FAIL: TestFitKeepsTheBoardAndShrinksTheHeadsWhenTheViewShrinks (0.00s)
--- FAIL: TestMinViewIsTheSmallestPlayableBoardPlusHUD (0.00s)
--- FAIL: TestBoardOriginCentersTheBoard (0.00s)
--- FAIL: TestEveryHerdMemberIsDrawnAsItsOwnPigHead (0.00s)
panic: runtime error: slice bounds out of range [:1] with capacity 0 [recovered, repanicked]
FAIL	github.com/MichaelKinsy/pigpen/pig-snake/internal/scene	0.008s
--- FAIL: TestNewStartsWithASoloLeaderCenteredFacingRight (0.00s)
--- FAIL: TestStepMovesTheLeaderOneCellPerDirection (0.00s)
--- FAIL: TestEatingAddsOnePigHeadAndScores (0.00s)
--- FAIL: TestTheHerdFollowsTheLeaderInLine (0.00s)
--- FAIL: TestFoodNeverLandsOnTheHerdAndPicksTheNthFreeCell (0.00s)
--- FAIL: TestReverseIsIgnoredOnceThereIsAHerdButAllowedForALoneLeader (0.00s)
--- FAIL: TestTwoQuickTurnsBothApplyOnSuccessiveSteps (0.00s)
--- FAIL: TestTheTurnQueueHoldsAtMostTwo (0.00s)
--- FAIL: TestWallsEndTheGameAtEveryEdgeWithoutMovingTheLeader (0.00s)
--- FAIL: TestWrapCarriesTheLeaderAroundEveryEdge (0.00s)
--- FAIL: TestWrapFoodAcrossTheEdgeIsEaten (0.00s)
--- FAIL: TestRunningIntoTheHerdEndsTheGame (0.00s)
--- FAIL: TestTheVacatingTailCellIsFreeWhenNotEating (0.00s)
--- FAIL: TestTheTailCellIsNotFreeWhenEating (0.00s)
--- FAIL: TestFillingTheBoardWins (0.00s)
--- FAIL: TestPauseTogglesOnlyWhilePlaying (0.00s)
--- FAIL: TestHighScoreFollowsTheScore (0.00s)
--- FAIL: TestIntervalSpeedsUpWithTheHerdAndClamps (0.00s)
--- FAIL: TestModeSwitchesOnlyBeforeTheFirstStep (0.00s)
--- FAIL: TestResizeOnlyWhileWaitingAndKeepsTheHighScore (0.00s)
--- FAIL: TestRestartKeepsSizeModeAndHighAndStartsPlaying (0.00s)
--- FAIL: TestDirHelpers (0.00s)
FAIL
FAIL	github.com/MichaelKinsy/pigpen/pig-snake/internal/snake	0.006s
--- FAIL: TestBuiltinOffersEightSixAndFourPixelHeads (0.00s)
--- FAIL: TestTheEightPixelHeadIsTheAngryPigsPigVerbatim (0.00s)
panic: runtime error: invalid memory address or nil pointer dereference [recovered, repanicked]
FAIL	github.com/MichaelKinsy/pigpen/pig-snake/internal/sprites	0.007s
--- FAIL: TestParseKeyDecodesLegacyApplicationAndKitty (0.01s)
--- FAIL: TestViewportSubtractsTheOverlayBox (0.00s)
FAIL
FAIL	github.com/MichaelKinsy/pigpen/pig-snake/internal/termgame	0.020s
FAIL
```
