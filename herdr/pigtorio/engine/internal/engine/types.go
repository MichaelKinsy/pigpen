// Package engine implements the Pigtorio engine: crates, lines, stations and the crate log.
package engine

import "time"

// Version is the engine version written to state.json.
const Version = "0.1.0"

// Crate states.
const (
	StBoard   = "board"
	StWorking = "working"
	StBlocked = "blocked"
	StOven    = "oven"
	StBurnt   = "burnt"
	StHolding = "holding"
	StBarn    = "barn"
	StShipped = "shipped"
	StFailed  = "failed"
)

// At says where a crate sits.
type At struct {
	Kind string `json:"kind"`
	Pig  string `json:"pig,omitempty"`
}

// Hist is one line of a crate's history.
type Hist struct {
	T      int64  `json:"t"`
	What   string `json:"what"`
	Detail string `json:"detail,omitempty"`
}

// Crate is one job. Fields after Cost are engine bookkeeping that the mod may ignore.
type Crate struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Text      string   `json:"text"`
	DoneWhen  string   `json:"doneWhen"`
	Size      string   `json:"size"`
	Pen       string   `json:"pen"`
	AFK       bool     `json:"afk"`
	BlockedBy []string `json:"blockedBy"`
	AskedBy   string   `json:"askedBy"`
	Created   int64    `json:"created"`
	State     string   `json:"state"`
	Line      string   `json:"line,omitempty"`
	Stop      int      `json:"stop"`
	At        *At      `json:"at,omitempty"`
	Burns     int      `json:"burns"`
	Report    string   `json:"report"`
	Log       string   `json:"log"`
	PR        string   `json:"pr,omitempty"`
	Commit    string   `json:"commit,omitempty"`
	Tokens    int      `json:"tokens"`
	Cost      float64  `json:"cost"`
	History   []Hist   `json:"history"`
	Folder    string   `json:"folder"`  // where the Oven and the Barn work; "" while unknown
	Updated   int64    `json:"updated"` // ms of the last change to this crate

	Direct   bool   `json:"direct,omitempty"`   // handed to one pig, no line
	Sent     bool   `json:"sent,omitempty"`     // the brief reached the pig at this stop
	SentAt   int64  `json:"sentAt,omitempty"`   // ms when it did
	SentTurn int    `json:"sentTurn,omitempty"` // the pig's turn counter then
	HandFrom string `json:"handFrom,omitempty"` // who wrote Report (the hand-off that goes into the next brief)
	Note     string `json:"note,omitempty"`     // oven log or review note for the next brief
}

// PigSlot is one live terminal: the crate it holds (if any) and its reported status.
type PigSlot struct {
	Crate       string `json:"crate,omitempty"`
	Since       int64  `json:"since,omitempty"`
	Status      string `json:"status,omitempty"`
	StatusSince int64  `json:"statusSince,omitempty"`
}

// Ack answers one inbox command.
type Ack struct {
	ID    string `json:"id"`
	Op    string `json:"op"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// Stats cover the last 24 hours.
type Stats struct {
	CratesIn24h    int `json:"cratesIn24h"`
	TrufflesOut24h int `json:"trufflesOut24h"`
	Burnt24h       int `json:"burnt24h"`
	Tokens24h      int `json:"tokens24h"`
}

// EngineInfo is the heartbeat block.
type EngineInfo struct {
	PID     int    `json:"pid"`
	Started int64  `json:"started"`
	Beat    int64  `json:"beat"`
	Version string `json:"version"`
}

// State is state.json.
type State struct {
	Engine EngineInfo         `json:"engine"`
	Crates []*Crate           `json:"crates"`
	Pigs   map[string]PigSlot `json:"pigs"`
	Acks   []Ack              `json:"acks"`
	Stats  Stats              `json:"stats"`
}

// Event is one line of crates.jsonl.
type Event struct {
	T      int64   `json:"t"`
	Crate  string  `json:"crate"`
	Event  string  `json:"event"`
	Pig    string  `json:"pig,omitempty"`
	Detail string  `json:"detail,omitempty"`
	Tokens int     `json:"tokens,omitempty"`
	Cost   float64 `json:"cost,omitempty"`
}

// Farm is farm.json as far as the engine reads it. The engine never writes it.
type Farm struct {
	Pens  map[string]Pen     `json:"pens"`
	Pigs  map[string]FarmPig `json:"pigs"`
	Lines []Line             `json:"lines"`
}

// Pen is a farm pen: the folder (optional) and the Oven's test command.
type Pen struct {
	Test   string `json:"test"`
	Folder string `json:"folder"`
	Fence  int    `json:"fence"`
}

// FarmPig names a pig.
type FarmPig struct {
	Name string `json:"name"`
	Role string `json:"role"`
	Skin string `json:"skin"`
}

// Line is an ordered list of stops.
type Line struct {
	ID    string `json:"id"`
	Pen   string `json:"pen"`
	Stops []Stop `json:"stops"`
}

// Stop is one station on a line: pig, oven, review or barn.
type Stop struct {
	Kind string `json:"kind"`
	Pig  string `json:"pig,omitempty"`
}

// Command is one inbox command.
type Command struct {
	Op    string         `json:"op"`
	Crate *NewCrate      `json:"crate"`
	ID    string         `json:"id"`
	Patch map[string]any `json:"patch"`
	To    string         `json:"to"`
	Line  string         `json:"line"`
	Note  string         `json:"note"`
}

// NewCrate is the crate object of crate.add.
type NewCrate struct {
	// ID is optional: a caller that names the crate can send follow-up commands
	// (crate.hand, crate.run) before it reads the engine's state.
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Text      string   `json:"text"`
	DoneWhen  string   `json:"doneWhen"`
	Size      string   `json:"size"`
	Pen       string   `json:"pen"`
	AFK       bool     `json:"afk"`
	BlockedBy []string `json:"blockedBy"`
	AskedBy   string   `json:"askedBy"`
}

func ms(t time.Time) int64 { return t.UnixMilli() }
