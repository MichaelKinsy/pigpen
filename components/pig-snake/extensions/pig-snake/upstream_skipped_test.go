package pig_snake

import "testing"

// Named skipped twins. PiG d86eb93's pig-runner and angry-pigs have tests for
// mechanics Pig Snake does not have. Each one keeps its upstream name here so
// the gap is visible and counted (port/PORT.md), never silently dropped.

func skipUpstream(t *testing.T, why string) {
	t.Helper()
	t.Skip("gap: " + why)
}

// pigrunner/pigrunner_test.go
func TestJump(t *testing.T)                           { skipUpstream(t, "no jumping: the snake moves on a grid") }
func TestDuck(t *testing.T)                           { skipUpstream(t, "no ducking") }
func TestJumpClearsGroundObstacleAtApex(t *testing.T) { skipUpstream(t, "no obstacles or physics") }
func TestDuckClearsAirObstacle(t *testing.T)          { skipUpstream(t, "no obstacles or physics") }
func TestObstacleDensityRisesWithSpeed(t *testing.T) {
	skipUpstream(t, "no obstacles; speed is covered by TestIntervalSpeedsUpWithTheHerdAndClamps")
}
func TestUpdate_ScoreAdvances(t *testing.T) {
	skipUpstream(t, "score is apples eaten, not distance; see TestEatingAddsOnePigHeadAndScores")
}
func TestPieSpritesHaveUniformWidth(t *testing.T) {
	skipUpstream(t, "no pies; the apple art has TestAppleArtHasUniformRowsAndOnlyKnownSymbols")
}
func TestCustomVariantRunnerSprites(t *testing.T) {
	skipUpstream(t, "the Sheriff hat art is not drawn (only its colors); see TestTheLeaderWearsTheSpriteChosenWithSprite")
}
func TestTallViewKeepsTheGameBandAtTheBottom(t *testing.T) {
	skipUpstream(t, "the board is centered in the view, not bottom-anchored; see TestBoardOriginCentersTheBoard")
}

// pigrunner/human_bot_test.go: the human-like runner bot has no counterpart; the snake's bots are
// TestBotPlaythroughBuildsALongHerdAndKeepsItInLine and TestANaiveBotHitsTheWallInWallsModeAndRunsStraightForeverInWrap.
func TestHumanLikeBotDiesWithinABoundedScore(t *testing.T) {
	skipUpstream(t, "jump/duck timing bot; replaced by the snake bots in internal/snake/playthrough_test.go")
}

// angrypigs/game_test.go: aiming, launching, blocks, birds, levels, camera, mouse.
func TestAimStaysInBoundsAndFreezesDuringFlight(t *testing.T) { skipUpstream(t, "no aiming") }
func TestLaunchSpendsAPigOnlyWhenReady(t *testing.T)          { skipUpstream(t, "no launching") }
func TestBirdHitScoresAndBlockHitEndsTheShot(t *testing.T)    { skipUpstream(t, "no birds or blocks") }
func TestHardHitsDealDoubleDamage(t *testing.T)               { skipUpstream(t, "no damage model") }
func TestIceShattersOnAnyHit(t *testing.T)                    { skipUpstream(t, "no materials") }
func TestSettleDropsBlocksAndTumblingBirdsAreKnockedOut(t *testing.T) {
	skipUpstream(t, "no physics")
}
func TestEveryLevelStandsUntilHit(t *testing.T)   { skipUpstream(t, "no levels") }
func TestLintelsHoldUntilALegBreaks(t *testing.T) { skipUpstream(t, "no structures") }
func TestLevelFlowBonusNextLevelWinAndRestart(t *testing.T) {
	skipUpstream(t, "no levels; restart is TestSnakeRestartKeepsHighScoreModeAndSprite")
}
func TestRunningOutOfPigsEndsTheGame(t *testing.T)         { skipUpstream(t, "pigs are the herd, never spent") }
func TestEveryLevelIsClearable(t *testing.T)               { skipUpstream(t, "no levels") }
func TestPowerRaisesLaunchSpeedMonotonically(t *testing.T) { skipUpstream(t, "no launch power") }
func TestPowerKeysClampAtMinimumAndMaximum(t *testing.T)   { skipUpstream(t, "no launch power") }
func TestPreviewMatchesTheRealFlight(t *testing.T)         { skipUpstream(t, "no trajectory preview") }
func TestMouseDragPullsAndReleaseFires(t *testing.T) {
	skipUpstream(t, "keyboard only; see internal/termgame TestParseMouseDecodesSGRReports")
}
func TestPreviewTracesTheAimAndStopsAtStructures(t *testing.T) {
	skipUpstream(t, "no trajectory preview")
}
func TestCameraShowsTheFortressFollowsThePigAndReturns(t *testing.T) {
	skipUpstream(t, "no scrolling camera: the whole board is always visible or the game pauses")
}

// internal/arcade/arcade_test.go
func TestHillsMatchTheRunnerProfiles(t *testing.T) {
	skipUpstream(t, "no hills or sky gradient scenery: the board is a checkered field")
}
