package pig_snake

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// highScores are the best scores of the two modes.
type highScores struct{ High, WrapHigh int }

// savedState is the state file. Like PiG Runner's, it is small, strict and
// private to the user (0600).
type savedState struct {
	HighScore     int `json:"highScore"`
	WrapHighScore int `json:"wrapHighScore"`
}

func statePath(configHome string) string {
	return filepath.Join(configHome, "state", "pigpen", "pig-snake.json")
}

// loadHighScores reads the state file. Anything unexpected (missing, too
// large, unknown fields, trailing data, negative scores) reads as no scores.
func loadHighScores(configHome string) highScores {
	file, err := os.Open(statePath(configHome))
	if err != nil {
		return highScores{}
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.Size() > 4096 {
		return highScores{}
	}
	decoder := json.NewDecoder(io.LimitReader(file, 4096))
	decoder.DisallowUnknownFields()
	var state savedState
	if decoder.Decode(&state) != nil || state.HighScore < 0 || state.WrapHighScore < 0 {
		return highScores{}
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return highScores{}
	}
	return highScores{High: state.HighScore, WrapHigh: state.WrapHighScore}
}

// saveHighScores writes the state file atomically.
func saveHighScores(configHome string, scores highScores) error {
	if scores.High < 0 || scores.WrapHigh < 0 {
		return fmt.Errorf("high score must not be negative")
	}
	path := statePath(configHome)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create Pig Snake state directory: %w", err)
	}
	data, err := json.Marshal(savedState{HighScore: scores.High, WrapHighScore: scores.WrapHigh})
	if err != nil {
		return fmt.Errorf("encode Pig Snake state: %w", err)
	}
	temporary, err := os.CreateTemp(dir, ".pigsnake-*")
	if err != nil {
		return fmt.Errorf("create Pig Snake state: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("protect Pig Snake state: %w", err)
	}
	if _, err := temporary.Write(append(data, '\n')); err != nil {
		temporary.Close()
		return fmt.Errorf("write Pig Snake state: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync Pig Snake state: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close Pig Snake state: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		// Windows cannot rename over an existing file.
		if removeErr := os.Remove(path); removeErr != nil && !os.IsNotExist(removeErr) {
			return fmt.Errorf("remove previous Pig Snake state: %w", removeErr)
		}
		if retryErr := os.Rename(temporaryPath, path); retryErr != nil {
			return fmt.Errorf("replace Pig Snake state: %w", retryErr)
		}
	}
	return nil
}

// gameState is what the component hands back to the extension when the player
// leaves: the score of this game and the best scores of both modes.
type gameState struct {
	Score     int  `json:"score"`
	HighScore int  `json:"highScore"`
	WrapHigh  int  `json:"wrapHighScore"`
	Herd      int  `json:"herd"`
	Wrap      bool `json:"wrap"`
	GameOver  bool `json:"gameOver"`
}

// high is the best score of the mode this state was played in.
func (s gameState) high() int {
	if s.Wrap {
		return s.WrapHigh
	}
	return s.HighScore
}

// resultState merges the value the host returned for the overlay (a JSON
// object) over fallback, the component's own final state. Missing, mistyped
// and negative fields keep the fallback.
func resultState(result any, fallback gameState) gameState {
	state := fallback
	if values, ok := result.(map[string]any); ok {
		number := func(key string) (int, bool) {
			value, ok := values[key].(float64)
			return int(value), ok && value >= 0
		}
		herdSeen := false
		if v, ok := number("score"); ok {
			state.Score = v
		}
		if v, ok := number("highScore"); ok {
			state.HighScore = v
		}
		if v, ok := number("wrapHighScore"); ok {
			state.WrapHigh = v
		}
		if v, ok := number("herd"); ok {
			state.Herd, herdSeen = v, true
		}
		if v, ok := values["wrap"].(bool); ok {
			state.Wrap = v
		}
		if v, ok := values["gameOver"].(bool); ok {
			state.GameOver = v
		}
		if _, scored := number("score"); scored && !herdSeen {
			state.Herd = state.Score + 1
		}
	}
	// A score is always a best score of its mode.
	if state.Wrap {
		state.WrapHigh = max(state.WrapHigh, state.Score)
	} else {
		state.HighScore = max(state.HighScore, state.Score)
	}
	return state
}
