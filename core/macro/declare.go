package macro

// DeclareUsed appends a text variable declaration for every {{name}} the
// macro references but does not declare. It walks the same template strings
// as Validate: step values, locators, expectations and guard locators.
// Names are added in first-use order, without duplicates. Strings with a
// syntax error are skipped because Validate reports them. It returns the
// names it added.
func DeclareUsed(m *Macro) []string {
	have := make(map[string]bool, len(m.Variables))
	for _, d := range m.Variables {
		have[d.Name] = true
	}
	var added []string
	scan := func(s string) {
		parts, err := ParseExpr(s)
		if err != nil {
			return
		}
		for _, name := range Refs(parts) {
			if have[name] {
				continue
			}
			have[name] = true
			added = append(added, name)
			m.Variables = append(m.Variables, VariableDecl{Name: name, Type: VarString, Required: false})
		}
	}
	value := func(v *Value) {
		if v != nil {
			scan(v.Expr)
		}
	}
	locator := func(l *Locator) {
		scan(l.Value)
		if l.Kind == LocRole {
			scan(l.Name)
		}
	}
	target := func(t *Target) {
		if t == nil {
			return
		}
		for i := range t.Locators {
			locator(&t.Locators[i])
		}
	}
	for i := range m.Steps {
		s := &m.Steps[i]
		target(s.Target)
		value(s.Value)
		for j := range s.Expect {
			target(s.Expect[j].Target)
			value(s.Expect[j].Value)
		}
	}
	for i := range m.Guards {
		if m.Guards[i].Locator != nil {
			locator(m.Guards[i].Locator)
		}
	}
	return added
}
