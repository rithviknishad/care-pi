package service

import (
	"reflect"
	"testing"
)

// TestSetKeyEnv: "care-pi config set" keeps the text of an env value (a number stays a string), and a quoted YAML
// string loses its quotes (the Phase 4 exit test: CARE_PI_TEST_FAIL_MIGRATE="1" reached care-beat with the quotes).
func TestSetKeyEnv(t *testing.T) {
	cases := []struct{ value, want string }{
		{`1`, "1"},
		{`"1"`, "1"},
		{`'1'`, "1"},
		{`1.10`, "1.10"},
		{`0123`, "0123"},
		{`true`, "true"},
		{`5/10m`, "5/10m"},
		{`a #b`, "a #b"},
		{`"a b"`, "a b"},
		{`"say \"hi\""`, `say "hi"`},
	}
	for _, c := range cases {
		doc := map[string]any{}
		if err := SetKey(doc, "env.backend.K", c.value); err != nil {
			t.Fatalf("%s: %v", c.value, err)
		}
		if got := doc["env"].(map[string]any)["backend"].(map[string]any)["K"]; got != c.want {
			t.Errorf("SetKey(%s) = %#v, want %#v", c.value, got, c.want)
		}
	}
	doc := map[string]any{}
	if err := SetKey(doc, "updates.auto", "true"); err != nil || !reflect.DeepEqual(doc, map[string]any{"updates": map[string]any{"auto": true}}) {
		t.Fatalf("updates.auto: %v %#v", err, doc)
	}
	if err := SetKey(doc, "env.backend.K", "null"); err != nil {
		t.Fatal(err)
	}
}
