package lazyre

import "testing"

// A pattern is compiled on first use, not when the package is loaded: pig-music is loaded in every PiG session that selects it,
// and most never type /music (a bounded repeat such as {0,200} alone costs 170 KB to compile).
func TestACompileHappensOnFirstUseNotAtDeclaration(t *testing.T) {
	re := New(`^[a-z]{1,3}$`)
	if re.compiled() {
		t.Fatal("compiled at declaration")
	}
	if !re.MatchString("abc") || re.MatchString("abcd") {
		t.Fatal("wrong match")
	}
	if !re.compiled() {
		t.Fatal("not compiled after use")
	}
}

func TestABadPatternPanicsOnUseLikeMustCompile(t *testing.T) {
	re := New(`(`)
	defer func() {
		if recover() == nil {
			t.Fatal("no panic")
		}
	}()
	re.MatchString("x")
}

func TestTheUsedMethodsAgreeWithRegexp(t *testing.T) {
	re := New(`(\d+)\.(\d+)`)
	if got := re.FindStringSubmatch("v1.22x"); len(got) != 3 || got[1] != "1" || got[2] != "22" {
		t.Errorf("%v", got)
	}
	if got := re.ReplaceAllString("1.2 3.4", "<$1>"); got != "<1> <3>" {
		t.Errorf("%q", got)
	}
	if got := New(`,`).Split("a,b", -1); len(got) != 2 {
		t.Errorf("%v", got)
	}
	if got := re.FindAllString("1.2 3.4", -1); len(got) != 2 {
		t.Errorf("%v", got)
	}
}
