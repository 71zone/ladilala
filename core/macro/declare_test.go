package macro

import (
	"reflect"
	"testing"
)

func TestDeclareUsed(t *testing.T) {
	m := &Macro{
		Variables: []VariableDecl{{Name: "known", Type: VarNumber}},
		Steps: []Step{
			{ID: "a", Action: ActionFill,
				Target: &Target{Locators: []Locator{{Kind: LocLabel, Value: "{{loc}}"}}},
				Value:  &Value{Expr: "Hello {{a}} {{b}} {{known}}"}},
			{ID: "b", Action: ActionFill, Value: &Value{Expr: "{{a | trim}} {{c}}"}},
			{ID: "c", Action: ActionFill, Value: &Value{Expr: "{{bad"}},
		},
	}
	got := DeclareUsed(m)
	want := []string{"loc", "a", "b", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("added = %v, want %v", got, want)
	}
	if len(m.Variables) != 5 || m.Variables[0].Type != VarNumber {
		t.Fatalf("variables = %+v", m.Variables)
	}
	for _, d := range m.Variables[1:] {
		if d.Type != VarString || d.Required {
			t.Errorf("auto-declared %+v must be an optional string", d)
		}
	}
	if again := DeclareUsed(m); len(again) != 0 || len(m.Variables) != 5 {
		t.Errorf("second call must add nothing, added %v", again)
	}
}
