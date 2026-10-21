package powerline_footer

// fakeBase also stands in for the whole default editor (powerlineBase) in the tests that build a powerlineEditor without the
// wire; textBase overrides what it keeps state for.

func (f *fakeBase) GetText() (string, error)         { return "", nil }
func (f *fakeBase) GetExpandedText() (string, error) { return "", nil }
func (f *fakeBase) SetText(string) error             { return nil }
func (f *fakeBase) InsertTextAtCursor(string) error  { return nil }
func (f *fakeBase) AddToHistory(string) error        { return nil }
