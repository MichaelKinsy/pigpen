package pigrunner

import "math"

// The AI player's pace. A classifier needs about 0.7 s to answer, about 80 px of road at the
// start speed, so an answer computed from the obstacle's distance would arrive after the
// obstacle. With an AI playing, the run instead holds at each obstacle's decision point: when
// the next obstacle is decisionWindowMs from the pig, the game stops advancing until the AI
// sends its game_act for it, and the action lands on the frame the run resumes. The AI still
// has to read the state and choose: a wrong answer (or none) at the decision point crashes the
// pig, and a jump sent early lands early. Only the waiting is taken out of the contest, not
// the decision; nothing is scripted.

// decisionWindowMs is the time to impact, in milliseconds, at which the run holds for the AI
// player's decision. A jump or a duck started there clears every obstacle the game makes, at
// every speed (TestDecisionWindowClearsEveryObstacle).
const decisionWindowMs = 150

// nextObstacle is the first obstacle whose right edge is still ahead of the pig's back, the
// one game_state reports, or nil.
func (g *Game) nextObstacle() *Obstacle {
	for i := range g.Obstacles {
		if int(g.Obstacles[i].X)+g.Obstacles[i].width() > pigX+3 {
			return &g.Obstacles[i]
		}
	}
	return nil
}

// distanceToPig is the road between the obstacle's front and the pig's front, in playfield
// pixels, the distance_px of game_state.
func distanceToPig(ob *Obstacle) int { return max(int(ob.X)+1-(pigX+10), 0) }

// timeToImpactMs is how long the obstacle takes to reach the pig at the current speed, in
// milliseconds.
func (g *Game) timeToImpactMs(ob *Obstacle) int {
	perSecond := (g.pixelSpeed() + ob.SpeedOffset*dinoScale) * frameHz
	if perSecond <= 0 {
		return 0
	}
	return int(math.Round(float64(distanceToPig(ob)) / perSecond * 1000))
}

// holdForDecision reports whether the run holds for the AI player's decision, and whether the
// hold started now: it starts when the next undecided obstacle reaches its decision point.
func (g *Game) holdForDecision() (holding, started bool) {
	if !g.AIPaced {
		return false, false
	}
	if g.Holding {
		return true, false
	}
	ob := g.nextObstacle()
	if ob == nil || ob.decided || g.timeToImpactMs(ob) > decisionWindowMs {
		return false, false
	}
	g.Holding, g.holdSeen, g.holdAnswered = true, false, false
	return true, true
}

// observeForAgent is game_state: the facts, and, while the run holds, a note that the AI
// player has now seen the obstacle it holds for, so its next game_act answers for it.
func (g *Game) observeForAgent() map[string]any {
	if g.Holding {
		g.holdSeen = true
	}
	return g.agentFacts()
}

// answerReleases reports whether a game_act sent now answers the hold: the run holds, the AI
// player has read the state since it began, and no answer is on its way yet. An action read
// from an older state (still in flight when the hold began) is applied but does not release
// the run. Once it answered, game_state no longer asks for a decision, so a state read before
// the next frame applies the answer does not draw a second one.
func (g *Game) answerReleases() bool {
	if !g.Holding || !g.holdSeen || g.holdAnswered {
		return false
	}
	g.holdAnswered = true
	return true
}

// decideNow is game_state's decide_now: the run holds and waits for an answer.
func (g *Game) decideNow() bool { return g.Holding && !g.holdAnswered }

// releaseHold resumes the run after the AI player's answer.
func (g *Game) releaseHold() {
	if !g.Holding {
		return
	}
	if ob := g.nextObstacle(); ob != nil {
		ob.decided = true
	}
	g.Holding, g.holdSeen, g.holdAnswered = false, false, false
}
