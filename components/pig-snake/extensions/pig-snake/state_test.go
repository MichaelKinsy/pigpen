package pig_snake

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Twin of pigrunner TestRunnerHighScoreStateIsStrictAndProtected (PiG d86eb93),
// extended with the wrap-mode high score.
func TestSnakeHighScoreStateIsStrictAndProtected(t *testing.T) {
	configHome := t.TempDir()
	if err := saveHighScores(configHome, highScores{High: 42, WrapHigh: 7}); err != nil {
		t.Fatal(err)
	}
	if got := loadHighScores(configHome); got != (highScores{High: 42, WrapHigh: 7}) {
		t.Fatalf("loaded high scores = %+v", got)
	}
	info, err := os.Stat(statePath(configHome))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("state mode = %o", info.Mode().Perm())
	}
	dirInfo, err := os.Stat(filepath.Dir(statePath(configHome)))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("state directory mode = %o", dirInfo.Mode().Perm())
	}
	if err := os.WriteFile(statePath(configHome), []byte(`{"highScore":42,"private":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := loadHighScores(configHome); got != (highScores{}) {
		t.Fatalf("state with unknown field loaded %+v", got)
	}
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(statePath(configHome)), ".pigsnake-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("state write left temporary files: %v", matches)
	}
}

func TestSnakeStateLivesUnderTheConfigHomeAndRejectsBadFiles(t *testing.T) {
	home := t.TempDir()
	path := statePath(home)
	if !strings.HasPrefix(path, home) || !strings.HasSuffix(path, filepath.Join("state", "pigpen", "pig-snake.json")) {
		t.Fatalf("state path %q", path)
	}
	if got := loadHighScores(home); got != (highScores{}) {
		t.Fatalf("missing file: %+v", got)
	}
	if err := saveHighScores(home, highScores{High: -1}); err == nil {
		t.Fatal("a negative high score must not be saved")
	}
	if err := saveHighScores(home, highScores{WrapHigh: -1}); err == nil {
		t.Fatal("a negative wrap high score must not be saved")
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("a rejected save wrote a file")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"negative":      `{"highScore":-3,"wrapHighScore":1}`,
		"negative wrap": `{"highScore":3,"wrapHighScore":-1}`,
		"trailing":      `{"highScore":3,"wrapHighScore":1} {"x":1}`,
		"not json":      `garbage`,
		"oversize":      `{"highScore":3,"wrapHighScore":1}` + strings.Repeat(" ", 5000),
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := loadHighScores(home); got != (highScores{}) {
			t.Errorf("%s: loaded %+v", name, got)
		}
	}
	// A score saved before wrap mode existed still loads.
	if err := os.WriteFile(path, []byte(`{"highScore":9}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := loadHighScores(home); got != (highScores{High: 9}) {
		t.Fatalf("one-field file: %+v", got)
	}
	// Saving twice replaces the first file.
	if err := saveHighScores(home, highScores{High: 1}); err != nil {
		t.Fatal(err)
	}
	if err := saveHighScores(home, highScores{High: 2, WrapHigh: 3}); err != nil {
		t.Fatal(err)
	}
	if got := loadHighScores(home); got != (highScores{High: 2, WrapHigh: 3}) {
		t.Fatalf("second save: %+v", got)
	}
}

// Twin of pigrunner TestRunnerScoresUsesWireResultAndFallback.
func TestSnakeScoresUsesWireResultAndFallback(t *testing.T) {
	fallback := gameState{Score: 3, HighScore: 8}
	got := resultState(map[string]any{"score": float64(12), "highScore": float64(10)}, fallback)
	if got.Score != 12 || got.high() != 12 {
		t.Fatalf("wire scores = %d/%d", got.Score, got.high())
	}
	got = resultState(nil, fallback)
	if got.Score != 3 || got.high() != 8 {
		t.Fatalf("fallback scores = %d/%d", got.Score, got.high())
	}
}

func TestSnakeResultStateReadsEveryWireFieldAndRejectsBadOnes(t *testing.T) {
	fallback := gameState{Score: 3, HighScore: 8, WrapHigh: 5, Herd: 4}
	got := resultState(map[string]any{
		"score": float64(4), "highScore": float64(20), "wrapHighScore": float64(6), "herd": float64(5), "wrap": true, "gameOver": true,
	}, fallback)
	if got != (gameState{Score: 4, HighScore: 20, WrapHigh: 6, Herd: 5, Wrap: true, GameOver: true}) {
		t.Fatalf("state %+v", got)
	}
	if got.high() != 6 {
		t.Fatalf("in wrap mode the wrap high score counts: %d", got.high())
	}
	// A score above the mode's high raises that high, and only that one.
	got = resultState(map[string]any{"score": float64(30), "wrap": true}, fallback)
	if got.WrapHigh != 30 || got.HighScore != 8 {
		t.Fatalf("wrap score 30: %+v", got)
	}
	got = resultState(map[string]any{"score": float64(30)}, fallback)
	if got.HighScore != 30 || got.WrapHigh != 5 {
		t.Fatalf("walls score 30: %+v", got)
	}
	if got.Herd != 31 {
		t.Fatalf("a herd is the leader plus one pig per point: %d", got.Herd)
	}
	// Negative and mistyped values fall back.
	got = resultState(map[string]any{"score": float64(-1), "highScore": "x", "herd": -2.0}, fallback)
	if got.Score != 3 || got.HighScore != 8 || got.Herd != 4 {
		t.Fatalf("bad wire values: %+v", got)
	}
	if resultState("nope", fallback).Score != 3 {
		t.Fatal("non-object result must use the fallback")
	}
}
