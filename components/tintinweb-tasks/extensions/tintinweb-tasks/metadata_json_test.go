package tintinweb_tasks_test

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The original keeps metadata in a JavaScript object and prints it with JSON.stringify: array-index keys come
// first in ascending numeric order, numbers are written as JavaScript writes them, and the file is
// JSON.stringify(data, null, 2). (Insertion order of the other keys needs the model's argument order, which the
// SDK does not hand to a tool: see status/tintinweb-tasks.md.)
func TestMetadataIsWrittenAsJSONStringify(t *testing.T) {
	boot := func(t *testing.T) (*rig, string) {
		file := filepath.Join(t.TempDir(), "tasks.json")
		useEnv(t, "PI_TASKS", file)
		useConfig(t, obj{})
		return startRig(t, ""), file
	}
	t.Run("array-index keys come first, in numeric order", func(t *testing.T) {
		r, _ := boot(t)
		r.must("TaskCreate", obj{"subject": "S", "description": "d", "metadata": obj{"zeta": 1, "10": "ten", "2": "two", "alpha": obj{"y": 1, "3": 3, "1": 1}}})
		eqv(t, strings.Contains(r.must("TaskGet", obj{"taskId": "1"}), `Metadata: {"2":"two","10":"ten","alpha":{"1":1,"3":3,"y":1},"zeta":1}`), true)
	})
	t.Run("numbers are written as JSON.stringify writes them", func(t *testing.T) {
		r, _ := boot(t)
		r.must("TaskCreate", obj{"subject": "S", "description": "d", "metadata": obj{"neg": math.Copysign(0, -1), "big": 1e21, "small": 1e-7, "frac": 1.5, "n": 3}})
		eqv(t, strings.Contains(r.must("TaskGet", obj{"taskId": "1"}), `Metadata: {"big":1e+21,"frac":1.5,"n":3,"neg":0,"small":1e-7}`), true)
	})
	t.Run("the file holds the same order and numbers", func(t *testing.T) {
		r, file := boot(t)
		r.must("TaskCreate", obj{"subject": "S", "description": "d", "metadata": obj{"zeta": 1, "10": "ten", "2": "two", "neg": math.Copysign(0, -1)}})
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		eqv(t, strings.Contains(string(data), "\"metadata\": {\n        \"2\": \"two\",\n        \"10\": \"ten\",\n        \"neg\": 0,\n        \"zeta\": 1\n      }"), true)
	})
}
