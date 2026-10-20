package pigmodeltweaks

import "testing"

// The original reads settings.json, assigns two keys and writes it back with
// JSON.stringify(settings, null, 2): the user's key order survives, an existing
// key stays where it was, a new one goes last, and `<` is not escaped.
func TestSetTopLevelKeysKeepsOrderLikeJSONStringify(t *testing.T) {
	raw := []byte(`{"theme":"dark","defaultProvider":"old","packages":{"z":[1,2],"a":{}},"empty":[],"note":"a<b"}`)
	got, err := setTopLevelKeys(raw, [][2]string{{"defaultProvider", "openrouter"}, {"defaultModel", "vendor/m:deepseek"}})
	if err != nil {
		t.Fatal(err)
	}
	want := `{
  "theme": "dark",
  "defaultProvider": "openrouter",
  "packages": {
    "z": [
      1,
      2
    ],
    "a": {}
  },
  "empty": [],
  "note": "a<b",
  "defaultModel": "vendor/m:deepseek"
}
`
	if string(got) != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

func TestSetTopLevelKeysOnAnEmptyObject(t *testing.T) {
	got, err := setTopLevelKeys([]byte("{}\n"), [][2]string{{"defaultProvider", "acme"}, {"defaultModel", "m<1>"}})
	if err != nil {
		t.Fatal(err)
	}
	if want := "{\n  \"defaultProvider\": \"acme\",\n  \"defaultModel\": \"m<1>\"\n}\n"; string(got) != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestSetTopLevelKeysRefusesWhatIsNotAnObject(t *testing.T) {
	for _, raw := range []string{"", "[1]", `"x"`, `{"a":1} {"b":2}`, `{"a":`, "null"} {
		if _, err := setTopLevelKeys([]byte(raw), [][2]string{{"k", "v"}}); err == nil {
			t.Errorf("%q was accepted", raw)
		}
	}
}
